package handlers_elbv2

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mulgadc/spinifex/spinifex/albagent"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- evaluateHealth ---

func TestEvaluateHealth_InitialToHealthy(t *testing.T) {
	cfg := DefaultHealthCheck()
	ctr := &targetCounter{consecutiveHealthy: 1}

	state, desc := evaluateHealth(TargetHealthInitial, ctr, cfg)
	assert.Equal(t, TargetHealthHealthy, state)
	assert.Equal(t, "Target is healthy", desc)
}

func TestEvaluateHealth_InitialToUnhealthy(t *testing.T) {
	cfg := DefaultHealthCheck()
	ctr := &targetCounter{consecutiveUnhealthy: cfg.UnhealthyThreshold}

	state, _ := evaluateHealth(TargetHealthInitial, ctr, cfg)
	assert.Equal(t, TargetHealthUnhealthy, state)
}

func TestEvaluateHealth_InitialStaysInitial(t *testing.T) {
	cfg := DefaultHealthCheck()
	ctr := &targetCounter{consecutiveUnhealthy: 1} // below threshold

	state, _ := evaluateHealth(TargetHealthInitial, ctr, cfg)
	assert.Equal(t, TargetHealthInitial, state)
}

func TestEvaluateHealth_HealthyToUnhealthy(t *testing.T) {
	cfg := DefaultHealthCheck()
	ctr := &targetCounter{consecutiveUnhealthy: cfg.UnhealthyThreshold}

	state, _ := evaluateHealth(TargetHealthHealthy, ctr, cfg)
	assert.Equal(t, TargetHealthUnhealthy, state)
}

func TestEvaluateHealth_HealthyStaysHealthy(t *testing.T) {
	cfg := DefaultHealthCheck()
	ctr := &targetCounter{consecutiveUnhealthy: 1} // below threshold

	state, _ := evaluateHealth(TargetHealthHealthy, ctr, cfg)
	assert.Equal(t, TargetHealthHealthy, state)
}

func TestEvaluateHealth_UnhealthyToHealthy(t *testing.T) {
	cfg := DefaultHealthCheck()
	ctr := &targetCounter{consecutiveHealthy: cfg.HealthyThreshold}

	state, _ := evaluateHealth(TargetHealthUnhealthy, ctr, cfg)
	assert.Equal(t, TargetHealthHealthy, state)
}

func TestEvaluateHealth_UnhealthyStaysUnhealthy(t *testing.T) {
	cfg := DefaultHealthCheck()
	ctr := &targetCounter{consecutiveHealthy: cfg.HealthyThreshold - 1}

	state, _ := evaluateHealth(TargetHealthUnhealthy, ctr, cfg)
	assert.Equal(t, TargetHealthUnhealthy, state)
}

func TestEvaluateHealth_DrainingUnchanged(t *testing.T) {
	cfg := DefaultHealthCheck()
	ctr := &targetCounter{consecutiveHealthy: 100}

	state, _ := evaluateHealth(TargetHealthDraining, ctr, cfg)
	assert.Equal(t, TargetHealthDraining, state)
}

// --- integration: handleHealthReport via NATS ---

func setupTestNATS(t *testing.T) (*nats.Conn, *Store) {
	t.Helper()
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close() })

	store, err := NewStore(nc)
	require.NoError(t, err)
	return nc, store
}

func TestHandleHealthReport_TransitionsInitialToHealthy(t *testing.T) {
	nc, store := setupTestNATS(t)

	hc := newHealthChecker(store, nc)
	require.NoError(t, hc.start())
	t.Cleanup(func() { hc.stop() })

	tg := &TargetGroupRecord{
		TargetGroupArn: "arn:aws:elasticloadbalancing:us-east-1:000:targetgroup/test/tg-123",
		TargetGroupID:  "tg-123",
		Port:           80,
		HealthCheck:    DefaultHealthCheck(),
		Targets: []Target{
			{Id: "i-aaa111", Port: 80, HealthState: TargetHealthInitial, PrivateIP: "10.0.1.10"},
		},
	}
	require.NoError(t, store.PutTargetGroup(tg))

	// Publish a health report with the target UP
	report := albagent.HealthReport{
		LBID: "lb-test1",
		Servers: []albagent.ServerStatus{
			{Backend: "bk_tg-123", Server: sanitizeName("srv", "i-aaa111"), Status: "UP"},
		},
	}
	data, _ := json.Marshal(report)
	require.NoError(t, nc.Publish("elbv2.alb.lb-test1.health", data))
	nc.Flush()

	// Wait for the NATS message to be processed
	time.Sleep(100 * time.Millisecond)

	stored, err := store.GetTargetGroup("tg-123")
	require.NoError(t, err)
	assert.Equal(t, TargetHealthHealthy, stored.Targets[0].HealthState)
}

func TestHandleHealthReport_UnhealthyAfterThreshold(t *testing.T) {
	nc, store := setupTestNATS(t)

	hc := newHealthChecker(store, nc)
	require.NoError(t, hc.start())
	t.Cleanup(func() { hc.stop() })

	tg := &TargetGroupRecord{
		TargetGroupArn: "arn:aws:elasticloadbalancing:us-east-1:000:targetgroup/test/tg-456",
		TargetGroupID:  "tg-456",
		Port:           80,
		HealthCheck: HealthCheckConfig{
			UnhealthyThreshold: 2,
			HealthyThreshold:   5,
		},
		Targets: []Target{
			{Id: "i-bbb222", Port: 80, HealthState: TargetHealthInitial, PrivateIP: "10.0.1.11"},
		},
	}
	require.NoError(t, store.PutTargetGroup(tg))

	srvName := sanitizeName("srv", "i-bbb222")

	// Send 2 DOWN reports to hit the unhealthy threshold of 2
	for range 2 {
		report := albagent.HealthReport{
			LBID: "lb-test2",
			Servers: []albagent.ServerStatus{
				{Backend: "bk_tg-456", Server: srvName, Status: "DOWN"},
			},
		}
		data, _ := json.Marshal(report)
		require.NoError(t, nc.Publish("elbv2.alb.lb-test2.health", data))
		nc.Flush()
		time.Sleep(50 * time.Millisecond)
	}

	stored, err := store.GetTargetGroup("tg-456")
	require.NoError(t, err)
	assert.Equal(t, TargetHealthUnhealthy, stored.Targets[0].HealthState)
}

func TestHandleHealthReport_SkipsDrainingTargets(t *testing.T) {
	nc, store := setupTestNATS(t)

	hc := newHealthChecker(store, nc)
	require.NoError(t, hc.start())
	t.Cleanup(func() { hc.stop() })

	tg := &TargetGroupRecord{
		TargetGroupArn: "arn:aws:elasticloadbalancing:us-east-1:000:targetgroup/test/tg-789",
		TargetGroupID:  "tg-789",
		Port:           80,
		HealthCheck:    DefaultHealthCheck(),
		Targets: []Target{
			{Id: "i-drain", Port: 80, HealthState: TargetHealthDraining, PrivateIP: "10.0.0.1"},
		},
	}
	require.NoError(t, store.PutTargetGroup(tg))

	report := albagent.HealthReport{
		LBID: "lb-test3",
		Servers: []albagent.ServerStatus{
			{Backend: "bk_tg-789", Server: sanitizeName("srv", "i-drain"), Status: "UP"},
		},
	}
	data, _ := json.Marshal(report)
	require.NoError(t, nc.Publish("elbv2.alb.lb-test3.health", data))
	nc.Flush()
	time.Sleep(100 * time.Millisecond)

	stored, err := store.GetTargetGroup("tg-789")
	require.NoError(t, err)
	assert.Equal(t, TargetHealthDraining, stored.Targets[0].HealthState)
}

func TestRemoveTarget(t *testing.T) {
	hc := newHealthChecker(nil, nil)

	hc.mu.Lock()
	hc.counters["tg-1:i-aaa:80"] = &targetCounter{consecutiveHealthy: 5}
	hc.mu.Unlock()

	hc.removeTarget("tg-1", "i-aaa", 80)

	hc.mu.Lock()
	_, exists := hc.counters["tg-1:i-aaa:80"]
	hc.mu.Unlock()
	assert.False(t, exists)
}
