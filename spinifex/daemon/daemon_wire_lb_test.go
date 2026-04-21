package daemon

import (
	"testing"

	"github.com/mulgadc/spinifex/spinifex/config"
	handlers_elbv2 "github.com/mulgadc/spinifex/spinifex/handlers/elbv2"
	"github.com/mulgadc/spinifex/spinifex/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWireLBTestDaemon creates a minimal Daemon with an ELBv2 service for
// wireLBAgentConfig tests. Only the fields read by wireLBAgentConfig are set.
func newWireLBTestDaemon(t *testing.T, cfg *config.Config) *Daemon {
	t.Helper()
	_, nc, _ := testutil.StartTestJetStream(t)

	svc, err := handlers_elbv2.NewELBv2ServiceImplWithNATS(nil, nc)
	require.NoError(t, err)
	t.Cleanup(func() { svc.Close() })

	return &Daemon{
		config:       cfg,
		elbv2Service: svc,
	}
}

func TestWireLBAgentConfig_Credentials(t *testing.T) {
	cfg := &config.Config{
		Predastore: config.PredastoreConfig{
			AccessKey: "AKID-test",
			SecretKey: "SK-test",
		},
	}
	d := newWireLBTestDaemon(t, cfg)

	d.wireLBAgentConfig()

	assert.Equal(t, "AKID-test", d.systemAccessKey)
	assert.Equal(t, "SK-test", d.systemSecretKey)
}

func TestWireLBAgentConfig_NoCredentials(t *testing.T) {
	cfg := &config.Config{}
	d := newWireLBTestDaemon(t, cfg)

	d.wireLBAgentConfig()

	assert.Empty(t, d.systemAccessKey)
	assert.Empty(t, d.systemSecretKey)
}

func TestWireLBAgentConfig_GatewayURL_MultiNode(t *testing.T) {
	// Multi-node: mgmtBridgeIP set, AWSGW binds to a specific WAN IP.
	// Gateway should use the AWSGW bind IP, not br-mgmt.
	cfg := &config.Config{
		AWSGW: config.AWSGWConfig{Host: "192.168.1.10:9999"},
	}
	d := newWireLBTestDaemon(t, cfg)
	d.mgmtBridgeIP = "10.15.8.1"

	d.wireLBAgentConfig()

	assert.Equal(t, "192.168.1.10", d.mgmtRouteVia)
}

func TestWireLBAgentConfig_GatewayURL_SingleNode(t *testing.T) {
	// Single-node: mgmtBridgeIP set, AWSGW on 0.0.0.0 → use br-mgmt IP.
	cfg := &config.Config{
		AWSGW: config.AWSGWConfig{Host: "0.0.0.0:9999"},
	}
	d := newWireLBTestDaemon(t, cfg)
	d.mgmtBridgeIP = "10.15.8.1"

	d.wireLBAgentConfig()

	assert.Empty(t, d.mgmtRouteVia)
}

func TestWireLBAgentConfig_GatewayURL_MgmtNoAWSGW(t *testing.T) {
	// mgmtBridgeIP set, no AWSGW Host → use br-mgmt IP.
	cfg := &config.Config{}
	d := newWireLBTestDaemon(t, cfg)
	d.mgmtBridgeIP = "10.15.8.1"

	d.wireLBAgentConfig()

	assert.Empty(t, d.mgmtRouteVia)
}

func TestWireLBAgentConfig_GatewayURL_DevNetworking(t *testing.T) {
	cfg := &config.Config{
		Daemon: config.DaemonConfig{DevNetworking: true},
	}
	d := newWireLBTestDaemon(t, cfg)

	d.wireLBAgentConfig()

	// DevNetworking fallback uses host gateway 10.0.2.2
	assert.Empty(t, d.mgmtRouteVia)
}

func TestWireLBAgentConfig_GatewayURL_AWSGWBindOnly(t *testing.T) {
	// No mgmtBridgeIP, no DevNetworking, AWSGW binds to specific IP.
	cfg := &config.Config{
		AWSGW: config.AWSGWConfig{Host: "192.168.1.10:8443"},
	}
	d := newWireLBTestDaemon(t, cfg)

	d.wireLBAgentConfig()

	assert.Empty(t, d.mgmtRouteVia)
	assert.Equal(t, "https://192.168.1.10:8443", d.elbv2Service.GatewayURL)
}

func TestWireLBAgentConfig_GatewayURL_LoopbackBindNoMgmtBridge(t *testing.T) {
	// Production single-node without br-mgmt (mulga-siv-6 regression guard):
	// awsgw.host defaults to 127.0.0.1 and the host has no mgmt bridge. The
	// fallback must NOT write 127.0.0.1 into LB VM cloud-init — the VM's
	// loopback is its own, not the host's. GatewayURL stays empty and the
	// daemon logs an error (handled downstream).
	cfg := &config.Config{
		AWSGW: config.AWSGWConfig{Host: "127.0.0.1:9999"},
	}
	d := newWireLBTestDaemon(t, cfg)

	d.wireLBAgentConfig()

	assert.Empty(t, d.mgmtRouteVia)
	assert.Empty(t, d.elbv2Service.GatewayURL, "must not inject loopback as GatewayURL")
}

func TestWireLBAgentConfig_GatewayURL_NoHost(t *testing.T) {
	// No mgmtBridgeIP, no DevNetworking, no AWSGW → no gateway URL.
	cfg := &config.Config{}
	d := newWireLBTestDaemon(t, cfg)

	d.wireLBAgentConfig()

	assert.Empty(t, d.mgmtRouteVia)
}

func TestWireLBAgentConfig_PortExtraction(t *testing.T) {
	cfg := &config.Config{
		AWSGW: config.AWSGWConfig{Host: "192.168.1.10:8443"},
	}
	d := newWireLBTestDaemon(t, cfg)
	d.mgmtBridgeIP = "10.15.8.1"

	d.wireLBAgentConfig()

	// Port should be extracted from AWSGW Host, not default 9999
	assert.Equal(t, "192.168.1.10", d.mgmtRouteVia)
}

func TestWireLBAgentConfig_DefaultPort(t *testing.T) {
	// AWSGW Host without port → default 9999.
	// Use mgmtBridgeIP with no AWSGW bind IP so gateway uses br-mgmt.
	cfg := &config.Config{}
	d := newWireLBTestDaemon(t, cfg)
	d.mgmtBridgeIP = "10.15.8.1"

	d.wireLBAgentConfig()

	assert.Empty(t, d.mgmtRouteVia)
}

func TestWireLBAgentConfig_MgmtRoute(t *testing.T) {
	cfg := &config.Config{
		AWSGW: config.AWSGWConfig{Host: "192.168.1.10:9999"},
	}
	d := newWireLBTestDaemon(t, cfg)
	d.mgmtBridgeIP = "10.15.8.1"

	d.wireLBAgentConfig()

	assert.Equal(t, "192.168.1.10", d.mgmtRouteVia)
}

func TestWireLBAgentConfig_NoMgmtRoute(t *testing.T) {
	// Single-node: AWSGW on 0.0.0.0 → no mgmtRouteVia set.
	cfg := &config.Config{
		AWSGW: config.AWSGWConfig{Host: "0.0.0.0:9999"},
	}
	d := newWireLBTestDaemon(t, cfg)
	d.mgmtBridgeIP = "10.15.8.1"

	d.wireLBAgentConfig()

	assert.Empty(t, d.mgmtRouteVia)
}
