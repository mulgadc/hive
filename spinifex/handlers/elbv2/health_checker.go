package handlers_elbv2

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/mulgadc/spinifex/spinifex/albagent"
)

// healthChecker processes target health reports from ALB agents. Reports arrive
// via ALBAgentHeartbeat (the agent pushes health data on each heartbeat). This
// checker maps HAProxy backend server statuses back to registered targets and
// updates HealthState in the store.
//
// This mirrors the AWS model where the ALB itself health-checks the targets,
// rather than the control plane probing them directly.
type healthChecker struct {
	store *Store

	mu       sync.Mutex
	counters map[string]*targetCounter // key: "tgID:targetId:port"
}

// targetCounter tracks consecutive pass/fail counts for threshold logic.
type targetCounter struct {
	consecutiveHealthy   int64
	consecutiveUnhealthy int64
}

func newHealthChecker(store *Store) *healthChecker {
	return &healthChecker{
		store:    store,
		counters: make(map[string]*targetCounter),
	}
}

// start is a no-op — health reports are now delivered directly by
// ALBAgentHeartbeat rather than polled over HTTP.
func (hc *healthChecker) start() error {
	return nil
}

// stop is a no-op — no background goroutine to stop.
func (hc *healthChecker) stop() {}

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
