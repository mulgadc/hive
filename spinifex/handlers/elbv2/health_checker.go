package handlers_elbv2

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mulgadc/spinifex/spinifex/albagent"
)

const healthPollInterval = 10 * time.Second

// healthChecker polls ALB agents over HTTP for target health reports. Each
// active ALB VM exposes GET /health which returns HAProxy backend server
// status. This checker maps those statuses back to registered targets and
// updates HealthState in the store.
//
// This mirrors the AWS model where the ALB itself health-checks the targets,
// rather than the control plane probing them directly.
type healthChecker struct {
	store      *Store
	httpClient *http.Client
	agentURLFn func(vpcIP string) string // for testing: override agent URL resolution

	mu       sync.Mutex
	counters map[string]*targetCounter // key: "tgID:targetId:port"
	stopCh   chan struct{}
}

// targetCounter tracks consecutive pass/fail counts for threshold logic.
type targetCounter struct {
	consecutiveHealthy   int64
	consecutiveUnhealthy int64
}

func newHealthChecker(store *Store) *healthChecker {
	return &healthChecker{
		store:      store,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		agentURLFn: agentURL,
		counters:   make(map[string]*targetCounter),
		stopCh:     make(chan struct{}),
	}
}

// start launches the background polling goroutine.
func (hc *healthChecker) start() error {
	go hc.pollLoop()
	return nil
}

// pollLoop periodically queries each active ALB's /health endpoint.
func (hc *healthChecker) pollLoop() {
	ticker := time.NewTicker(healthPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-hc.stopCh:
			return
		case <-ticker.C:
			hc.pollAll()
		}
	}
}

// pollAll fetches health from every active ALB that has a VPC IP.
func (hc *healthChecker) pollAll() {
	lbs, err := hc.store.ListLoadBalancers()
	if err != nil {
		slog.Debug("healthChecker: failed to list load balancers", "err", err)
		return
	}

	for _, lb := range lbs {
		if lb.State != StateActive || lb.VPCIP == "" {
			continue
		}
		hc.pollOne(lb.VPCIP)
	}
}

// pollOne fetches and processes the health report from a single ALB agent.
func (hc *healthChecker) pollOne(vpcIP string) {
	resp, err := hc.httpClient.Get(hc.agentURLFn(vpcIP) + "/health")
	if err != nil {
		slog.Debug("healthChecker: failed to poll ALB agent", "vpcIp", vpcIP, "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	hc.handleHealthReport(data)
}

// stop signals the polling goroutine to exit.
func (hc *healthChecker) stop() {
	close(hc.stopCh)
}

// handleHealthReport processes a health report from an alb-agent.
func (hc *healthChecker) handleHealthReport(data []byte) {
	var report albagent.HealthReport
	if err := json.Unmarshal(data, &report); err != nil {
		slog.Debug("healthChecker: invalid health report", "err", err)
		return
	}

	if len(report.Servers) == 0 {
		return
	}

	// Build a map of HAProxy server name → UP/DOWN status
	serverUp := make(map[string]bool, len(report.Servers))
	for _, srv := range report.Servers {
		serverUp[srv.Server] = srv.Status == "UP"
	}

	// Find all target groups and match servers by name
	tgs, err := hc.store.ListTargetGroups()
	if err != nil {
		slog.Debug("healthChecker: failed to list target groups", "err", err)
		return
	}

	hc.mu.Lock()
	defer hc.mu.Unlock()

	for _, tg := range tgs {
		changed := false
		for i := range tg.Targets {
			target := &tg.Targets[i]

			if target.PrivateIP == "" || target.HealthState == TargetHealthDraining {
				continue
			}

			// HAProxy server name matches the sanitized target ID
			srvName := sanitizeName("srv", target.Id)
			healthy, exists := serverUp[srvName]
			if !exists {
				continue
			}

			port := target.Port
			if port == 0 {
				port = tg.Port
			}

			key := fmt.Sprintf("%s:%s:%d", tg.TargetGroupID, target.Id, port)
			ctr, ok := hc.counters[key]
			if !ok {
				ctr = &targetCounter{}
				hc.counters[key] = ctr
			}

			if healthy {
				ctr.consecutiveHealthy++
				ctr.consecutiveUnhealthy = 0
			} else {
				ctr.consecutiveUnhealthy++
				ctr.consecutiveHealthy = 0
			}

			newState, newDesc := evaluateHealth(target.HealthState, ctr, tg.HealthCheck)
			if newState != target.HealthState {
				slog.Info("Target health changed",
					"targetId", target.Id,
					"from", target.HealthState,
					"to", newState,
				)
				target.HealthState = newState
				target.HealthDesc = newDesc
				changed = true
			}
		}

		if changed {
			if err := hc.store.PutTargetGroup(tg); err != nil {
				slog.Error("healthChecker: failed to persist target group", "tgId", tg.TargetGroupID, "err", err)
			}
		}
	}
}

// evaluateHealth applies threshold logic to determine a target's new state.
// From "initial", a single healthy probe transitions to healthy (fast start,
// matching AWS behavior for newly registered targets). From "healthy" or
// "unhealthy", full threshold counts are required.
func evaluateHealth(current string, ctr *targetCounter, cfg HealthCheckConfig) (string, string) {
	healthyThreshold := cfg.HealthyThreshold
	if healthyThreshold == 0 {
		healthyThreshold = DefaultHealthyThreshold
	}
	unhealthyThreshold := cfg.UnhealthyThreshold
	if unhealthyThreshold == 0 {
		unhealthyThreshold = DefaultUnhealthyThreshold
	}

	switch current {
	case TargetHealthInitial:
		if ctr.consecutiveHealthy >= 1 {
			return TargetHealthHealthy, "Target is healthy"
		}
		if ctr.consecutiveUnhealthy >= unhealthyThreshold {
			return TargetHealthUnhealthy, "Health check failed"
		}
		return current, "Target registration is in progress"

	case TargetHealthHealthy:
		if ctr.consecutiveUnhealthy >= unhealthyThreshold {
			return TargetHealthUnhealthy, "Health check failed"
		}
		return current, "Target is healthy"

	case TargetHealthUnhealthy:
		if ctr.consecutiveHealthy >= healthyThreshold {
			return TargetHealthHealthy, "Target is healthy"
		}
		return current, "Health check failed"

	default:
		return current, ""
	}
}

// removeTarget cleans up counters for a deregistered target.
func (hc *healthChecker) removeTarget(tgID, targetId string, port int64) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	delete(hc.counters, fmt.Sprintf("%s:%s:%d", tgID, targetId, port))
}
