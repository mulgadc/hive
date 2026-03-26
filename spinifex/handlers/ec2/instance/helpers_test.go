package handlers_ec2_instance

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- generateNetworkConfig ---

func TestGenerateNetworkConfig_BothEmpty(t *testing.T) {
	cfg := generateNetworkConfig("", "")
	assert.Equal(t, cloudInitNetworkConfigWildcard, cfg)
}

func TestGenerateNetworkConfig_OneEmpty(t *testing.T) {
	cfg := generateNetworkConfig("02:00:00:aa:bb:cc", "")
	assert.Equal(t, cloudInitNetworkConfigWildcard, cfg, "should fall back to wildcard if devMAC empty")

	cfg = generateNetworkConfig("", "02:00:00:dd:ee:ff")
	assert.Equal(t, cloudInitNetworkConfigWildcard, cfg, "should fall back to wildcard if eniMAC empty")
}

func TestGenerateNetworkConfig_DualNIC(t *testing.T) {
	cfg := generateNetworkConfig("02:00:00:aa:bb:cc", "02:00:00:dd:ee:ff")
	assert.Contains(t, cfg, "version: 2")
	assert.Contains(t, cfg, `macaddress: "02:00:00:aa:bb:cc"`)
	assert.Contains(t, cfg, `macaddress: "02:00:00:dd:ee:ff"`)
	assert.Contains(t, cfg, "use-routes: false")
	assert.Contains(t, cfg, "use-dns: false")
	assert.Contains(t, cfg, "vpc0:")
	assert.Contains(t, cfg, "dev0:")
}
