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

// AdvertiseIP covers the single-node default install (no br-mgmt, AWSGW on
// wildcard): the gateway URL falls back to the node's advertised off-host IP.
// Replaces the siv-6 "empty gateway URL" silent failure.
func TestWireLBAgentConfig_GatewayURL_AdvertiseFallback(t *testing.T) {
	cfg := &config.Config{
		AdvertiseIP: "192.168.1.21",
		AWSGW:       config.AWSGWConfig{Host: "0.0.0.0:9999"},
	}
	d := newWireLBTestDaemon(t, cfg)

	d.wireLBAgentConfig()

	assert.Equal(t, "https://192.168.1.21:9999", d.elbv2Service.GatewayURL)
	assert.Empty(t, d.mgmtRouteVia)
}

// When there's no br-mgmt, no AdvertiseIP, no DevNetworking, and AWSGW is on
// the wildcard, the gateway URL must stay empty and the daemon must log an
// error (rather than silently assigning a broken URL like pre-siv-8).
func TestWireLBAgentConfig_GatewayURL_NoAdvertiseNoBridge(t *testing.T) {
	cfg := &config.Config{
		AWSGW: config.AWSGWConfig{Host: "0.0.0.0:9999"},
	}
	d := newWireLBTestDaemon(t, cfg)

	d.wireLBAgentConfig()

	assert.Empty(t, d.elbv2Service.GatewayURL)
}

// Single-node local dev: AWSGW on 0.0.0.0, br-mgmt present, AdvertiseIP set.
// Gateway must be AdvertiseIP (reachable via the VM's WAN NIC), NOT the
// br-mgmt IP (guest has no DHCP on br-mgmt so that subnet is unreachable).
func TestWireLBAgentConfig_GatewayURL_AdvertiseOverMgmtBridge(t *testing.T) {
	cfg := &config.Config{
		AdvertiseIP: "192.168.1.32",
		AWSGW:       config.AWSGWConfig{Host: "0.0.0.0:9999"},
	}
	d := newWireLBTestDaemon(t, cfg)
	d.mgmtBridgeIP = "10.14.8.1"

	d.wireLBAgentConfig()

	assert.Equal(t, "https://192.168.1.32:9999", d.elbv2Service.GatewayURL)
	assert.Empty(t, d.mgmtRouteVia)
	assert.Empty(t, d.elbv2Service.MgmtRouteGateway)
	assert.Empty(t, d.elbv2Service.MgmtRouteTarget)
}

// Single-node with br-mgmt AND a specific AWSGW bind that matches AdvertiseIP
// (e.g. `spx admin init --bind $PRIMARY_WAN`): the WAN IP is reachable via the
// normal VPC → external path, so the bootcmd mgmt host route must NOT be
// injected. Injecting it would divert the reply leg of host-initiated ALB
// connections out br-mgmt, bypassing OVN SNAT and arriving at the host with a
// source that doesn't match the open TCP socket.
func TestWireLBAgentConfig_GatewayURL_SingleNodeBindMatchesAdvertise(t *testing.T) {
	cfg := &config.Config{
		AdvertiseIP: "192.168.0.17",
		AWSGW:       config.AWSGWConfig{Host: "192.168.0.17:9999"},
	}
	d := newWireLBTestDaemon(t, cfg)
	d.mgmtBridgeIP = "10.15.8.1"

	d.wireLBAgentConfig()

	assert.Equal(t, "https://192.168.0.17:9999", d.elbv2Service.GatewayURL)
	assert.Empty(t, d.mgmtRouteVia)
	assert.Empty(t, d.elbv2Service.MgmtRouteGateway)
	assert.Empty(t, d.elbv2Service.MgmtRouteTarget)
}
