package handlers_elbv2

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/mulgadc/spinifex/spinifex/albagent"
	"github.com/nats-io/nats.go"
)

// healthChecker subscribes to health reports published by alb-agents running
// inside ALB VMs. Each agent periodically queries HAProxy's stats socket and
// publishes backend server status. This checker maps those statuses back to
// registered targets and updates HealthState in the store.
//
// This mirrors the AWS model where the ALB itself health-checks the targets,
// rather than the control plane probing them directly.
type healthChecker struct {
	store *Store
	nc    *nats.Conn

	mu       sync.Mutex
	counters map[string]*targetCounter // key: "tgID:targetId:port"
	sub      *nats.Subscription
}

// targetCounter tracks consecutive pass/fail counts for threshold logic.
type targetCounter struct {
	consecutiveHealthy   int64
	consecutiveUnhealthy int64
}

func newHealthChecker(store *Store, nc *nats.Conn) *healthChecker {
	return &healthChecker{
		store:    store,
		nc:       nc,
		counters: make(map[string]*targetCounter),
	}
}

// start subscribes to all ALB health report topics via a wildcard subscription.
func (hc *healthChecker) start() error {
	if hc.nc == nil {
		return nil
	}

	// Wildcard subscribe to all ALB health reports: elbv2.alb.*.health
	sub, err := hc.nc.Subscribe("elbv2.alb.*.health", hc.handleHealthReport)
	if err != nil {
		return fmt.Errorf("subscribe to health reports: %w", err)
	}
	hc.sub = sub
	return nil
}

// stop unsubscribes from the health topic.
func (hc *healthChecker) stop() {
	if hc.sub != nil {
		hc.sub.Unsubscribe()
	}
}

// handleHealthReport processes a health report from an alb-agent.
func (hc *healthChecker) handleHealthReport(msg *nats.Msg) {
	var report albagent.HealthReport
	if err := json.Unmarshal(msg.Data, &report); err != nil {
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
