package vpcd

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Additional edge-case tests for subnetGateway and generateMAC.
// Core happy-path tests are in topology_test.go.

func TestSubnetGateway_Slash28(t *testing.T) {
	gw, prefix, err := subnetGateway("172.31.0.0/28")
	require.NoError(t, err)
	assert.Equal(t, "172.31.0.1", gw)
	assert.Equal(t, 28, prefix)
}

func TestSubnetGateway_IPv6(t *testing.T) {
	_, _, err := subnetGateway("2001:db8::/32")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only IPv4 supported")
}

func TestGenerateMAC_EmptyString(t *testing.T) {
	mac := generateMAC("")
	hw, err := net.ParseMAC(mac)
	require.NoError(t, err)
	// LAA + unicast bits enforced even for empty input.
	assert.Equal(t, byte(0x02), hw[0]&0x03)
	// Old 24-bit impl produced 02:00:00:00:00:00 (all-zero hash region) for
	// empty input. New impl hashes the prefix-and-id, so output must differ.
	assert.NotEqual(t, "02:00:00:00:00:00", hw.String())
}

// --- dnsServer ---

func TestDnsServer_WithDNSServers(t *testing.T) {
	pools := []ExternalPoolConfig{
		{Name: "test", DNSServers: []string{"10.0.0.2", "10.0.0.3"}},
	}
	h := NewTopologyHandler(nil, WithExternalNetwork("pool", pools))
	assert.Equal(t, "{10.0.0.2, 10.0.0.3}", h.dnsServer())
}

func TestDnsServer_SingleDNS(t *testing.T) {
	pools := []ExternalPoolConfig{
		{Name: "test", DNSServers: []string{"1.2.3.4"}},
	}
	h := NewTopologyHandler(nil, WithExternalNetwork("pool", pools))
	assert.Equal(t, "{1.2.3.4}", h.dnsServer())
}

func TestDnsServer_NoPools(t *testing.T) {
	h := NewTopologyHandler(nil)
	assert.Equal(t, "{8.8.8.8, 1.1.1.1}", h.dnsServer())
}

func TestDnsServer_PoolWithNoDNS(t *testing.T) {
	pools := []ExternalPoolConfig{
		{Name: "test"},
	}
	h := NewTopologyHandler(nil, WithExternalNetwork("pool", pools))
	assert.Equal(t, "{8.8.8.8, 1.1.1.1}", h.dnsServer())
}

// --- isMacvlanMode ---

func TestIsMacvlanMode_Default(t *testing.T) {
	h := NewTopologyHandler(nil)
	assert.True(t, h.isMacvlanMode(), "default (empty) should be macvlan")
}

func TestIsMacvlanMode_Direct(t *testing.T) {
	h := NewTopologyHandler(nil, WithBridgeMode(BridgeModeDirect))
	assert.False(t, h.isMacvlanMode())
}

func TestIsMacvlanMode_Macvlan(t *testing.T) {
	h := NewTopologyHandler(nil, WithBridgeMode(BridgeModeMacvlan))
	assert.True(t, h.isMacvlanMode())
}

func TestIsMacvlanMode_Veth(t *testing.T) {
	h := NewTopologyHandler(nil, WithBridgeMode(BridgeModeVeth))
	assert.False(t, h.isMacvlanMode(), "veth mode is not macvlan")
}

// --- useCentralizedNAT ---

func TestUseCentralizedNAT_Default(t *testing.T) {
	h := NewTopologyHandler(nil)
	assert.True(t, h.useCentralizedNAT(), "default (empty) should use centralized NAT")
}

func TestUseCentralizedNAT_Macvlan(t *testing.T) {
	h := NewTopologyHandler(nil, WithBridgeMode(BridgeModeMacvlan))
	assert.True(t, h.useCentralizedNAT())
}

func TestUseCentralizedNAT_Veth(t *testing.T) {
	h := NewTopologyHandler(nil, WithBridgeMode(BridgeModeVeth))
	assert.True(t, h.useCentralizedNAT(), "veth mode should use centralized NAT")
}

func TestUseCentralizedNAT_Direct(t *testing.T) {
	h := NewTopologyHandler(nil, WithBridgeMode(BridgeModeDirect))
	assert.False(t, h.useCentralizedNAT(), "direct bridge should use distributed NAT")
}
