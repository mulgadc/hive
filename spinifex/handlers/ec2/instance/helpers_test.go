package handlers_ec2_instance

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- generateNetworkConfig ---

func TestGenerateNetworkConfig_BothEmpty(t *testing.T) {
	cfg := generateNetworkConfig("", "", "", "")
	assert.Equal(t, cloudInitNetworkConfigWildcard, cfg)
}

func TestGenerateNetworkConfig_OneEmpty(t *testing.T) {
	cfg := generateNetworkConfig("02:00:00:aa:bb:cc", "", "", "")
	assert.Equal(t, cloudInitNetworkConfigWildcard, cfg, "should fall back to wildcard if devMAC empty")

	cfg = generateNetworkConfig("", "02:00:00:dd:ee:ff", "", "")
	assert.Equal(t, cloudInitNetworkConfigWildcard, cfg, "should fall back to wildcard if eniMAC empty")
}

func TestGenerateNetworkConfig_DualNIC(t *testing.T) {
	cfg := generateNetworkConfig("02:00:00:aa:bb:cc", "02:00:00:dd:ee:ff", "", "")
	assert.Contains(t, cfg, "version: 2")
	assert.Contains(t, cfg, `macaddress: "02:00:00:aa:bb:cc"`)
	assert.Contains(t, cfg, `macaddress: "02:00:00:dd:ee:ff"`)
	assert.Contains(t, cfg, "use-routes: false")
	assert.Contains(t, cfg, "use-dns: false")
	assert.Contains(t, cfg, "vpc0:")
	assert.Contains(t, cfg, "dev0:")
	assert.NotContains(t, cfg, "mgmt0:")
}

func TestGenerateNetworkConfig_TripleNIC(t *testing.T) {
	cfg := generateNetworkConfig("02:00:00:aa:bb:cc", "02:de:00:dd:ee:ff", "02:a0:00:11:22:33", "10.15.8.101")
	assert.Contains(t, cfg, "version: 2")
	assert.Contains(t, cfg, `macaddress: "02:00:00:aa:bb:cc"`)
	assert.Contains(t, cfg, `macaddress: "02:de:00:dd:ee:ff"`)
	assert.Contains(t, cfg, "mgmt0:")
	assert.Contains(t, cfg, `macaddress: "02:a0:00:11:22:33"`)
	assert.Contains(t, cfg, `"10.15.8.101/24"`)
	// mgmt NIC should not have DHCP or routes
	assert.Contains(t, cfg, "vpc0:")
	assert.Contains(t, cfg, "dev0:")
}

func TestGenerateNetworkConfig_MgmtWithoutDev(t *testing.T) {
	// mgmt NIC only applies when dual-NIC setup is active (eniMAC + devMAC both present)
	cfg := generateNetworkConfig("02:00:00:aa:bb:cc", "", "02:a0:00:11:22:33", "10.15.8.101")
	assert.Equal(t, cloudInitNetworkConfigWildcard, cfg, "should fall back to wildcard if devMAC empty even with mgmt")
}

func TestGenerateNetworkConfig_MgmtMACWithoutIP(t *testing.T) {
	cfg := generateNetworkConfig("02:00:00:aa:bb:cc", "02:de:00:dd:ee:ff", "02:a0:00:11:22:33", "")
	assert.NotContains(t, cfg, "mgmt0:", "mgmt NIC should not appear without IP")
}

func TestGenerateNetworkConfig_MgmtIPWithoutMAC(t *testing.T) {
	cfg := generateNetworkConfig("02:00:00:aa:bb:cc", "02:de:00:dd:ee:ff", "", "10.15.8.101")
	assert.NotContains(t, cfg, "mgmt0:", "mgmt NIC should not appear without MAC")
}
