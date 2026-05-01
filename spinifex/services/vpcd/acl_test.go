package vpcd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestACL_TCPPortFromCIDR(t *testing.T) {
	rule := SGRuleForACL{
		IpProtocol: "tcp",
		FromPort:   22,
		ToPort:     22,
		CidrIp:     "10.0.0.0/8",
	}
	match, err := BuildIngressACLMatch("sg_test", rule)
	require.NoError(t, err)
	assert.Contains(t, match, "tcp.dst == 22")
	assert.Contains(t, match, "ip4.src == 10.0.0.0/8")
	assert.Contains(t, match, "outport == @sg_test")
	assert.Contains(t, match, "ip4")
}

func TestACL_AllTrafficFromSG(t *testing.T) {
	rule := SGRuleForACL{
		IpProtocol: "-1",
		SourceSG:   "sg-abc123",
	}
	match, err := BuildIngressACLMatch("sg_test", rule)
	require.NoError(t, err)
	assert.Contains(t, match, "ip4.src == $sg_abc123_ip4")
	assert.Contains(t, match, "outport == @sg_test")
	assert.Contains(t, match, "ip4")
}

func TestACL_PortRange(t *testing.T) {
	rule := SGRuleForACL{
		IpProtocol: "udp",
		FromPort:   1024,
		ToPort:     65535,
	}
	match, err := BuildIngressACLMatch("sg_test", rule)
	require.NoError(t, err)
	assert.Contains(t, match, "udp.dst >= 1024")
	assert.Contains(t, match, "udp.dst <= 65535")
}

func TestACL_ICMP(t *testing.T) {
	rule := SGRuleForACL{
		IpProtocol: "icmp",
		CidrIp:     "0.0.0.0/0",
	}
	match, err := BuildIngressACLMatch("sg_test", rule)
	require.NoError(t, err)
	assert.Contains(t, match, "icmp4")
	assert.NotContains(t, match, "tcp.dst")
	assert.NotContains(t, match, "udp.dst")
}

func TestACL_AllProtocols(t *testing.T) {
	rule := SGRuleForACL{
		IpProtocol: "-1",
		CidrIp:     "10.0.0.0/16",
	}
	match, err := BuildIngressACLMatch("sg_test", rule)
	require.NoError(t, err)
	assert.Contains(t, match, "ip4")
	assert.Contains(t, match, "ip4.src == 10.0.0.0/16")
	assert.NotContains(t, match, "tcp")
	assert.NotContains(t, match, "udp")
	assert.NotContains(t, match, "icmp")
}

func TestACL_EgressAll(t *testing.T) {
	rule := SGRuleForACL{
		IpProtocol: "-1",
		CidrIp:     "0.0.0.0/0",
	}
	match, err := BuildEgressACLMatch("sg_test", rule)
	require.NoError(t, err)
	assert.Contains(t, match, "inport == @sg_test")
	assert.NotContains(t, match, "outport")
	assert.Contains(t, match, "ip4")
}

func TestACL_TCPSinglePort(t *testing.T) {
	rule := SGRuleForACL{
		IpProtocol: "tcp",
		FromPort:   443,
		ToPort:     443,
		CidrIp:     "10.0.0.0/8",
	}
	match, err := BuildIngressACLMatch("sg_test", rule)
	require.NoError(t, err)
	assert.Contains(t, match, "tcp.dst == 443")
	assert.NotContains(t, match, "tcp.dst >=")
	assert.NotContains(t, match, "tcp.dst <=")
}

func TestACL_NoSource(t *testing.T) {
	rule := SGRuleForACL{
		IpProtocol: "tcp",
		FromPort:   80,
		ToPort:     80,
		CidrIp:     "0.0.0.0/0",
	}
	match, err := BuildIngressACLMatch("sg_test", rule)
	require.NoError(t, err)
	assert.Contains(t, match, "tcp.dst == 80")
	assert.NotContains(t, match, "ip4.src")
}

// --- Defensive guard against OVN match-expression injection (Finding 1) ---

func TestACL_RejectsInjectionInCidrIp(t *testing.T) {
	payloads := []string{
		"1.2.3.4/32 || ip4.src == 0.0.0.0/0",
		"0.0.0.0/0; drop",
		"${jndi:ldap://x}",
		"10.0.0.0/8\n outport == @other",
		"10.0.0.0/8 && ip4.src == 0.0.0.0/0",
		"10.0.0.0/8\toutport==@x",
		"10.0.0.0/8\rfoo",
		"$evil",
		"@other_pg",
		"(1.2.3.4/32)",
		"10.0.0.0/8=foo",
	}
	for _, p := range payloads {
		t.Run(p, func(t *testing.T) {
			ingressRule := SGRuleForACL{IpProtocol: "tcp", FromPort: 80, ToPort: 80, CidrIp: p}
			_, err := BuildIngressACLMatch("sg_test", ingressRule)
			assert.Error(t, err, "BuildIngressACLMatch must reject %q", p)

			egressRule := SGRuleForACL{IpProtocol: "tcp", FromPort: 80, ToPort: 80, CidrIp: p}
			_, err = BuildEgressACLMatch("sg_test", egressRule)
			assert.Error(t, err, "BuildEgressACLMatch must reject %q", p)
		})
	}
}

func TestACL_RejectsInjectionInSourceSG(t *testing.T) {
	payloads := []string{
		"sg-abc || outport == @other",
		"sg-abc && ip4.src == 0.0.0.0/0",
		"sg-abc;drop",
		"sg-abc\noutport==@other",
		"sg-abc\tfoo",
		"@other_pg",
		"$injected",
		"sg-abc=bar",
	}
	for _, p := range payloads {
		t.Run(p, func(t *testing.T) {
			ingressRule := SGRuleForACL{IpProtocol: "-1", SourceSG: p}
			_, err := BuildIngressACLMatch("sg_test", ingressRule)
			assert.Error(t, err, "BuildIngressACLMatch must reject SourceSG %q", p)

			egressRule := SGRuleForACL{IpProtocol: "-1", SourceSG: p}
			_, err = BuildEgressACLMatch("sg_test", egressRule)
			assert.Error(t, err, "BuildEgressACLMatch must reject SourceSG %q", p)
		})
	}
}
