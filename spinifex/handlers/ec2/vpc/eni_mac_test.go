package handlers_ec2_vpc

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateENIMac_Deterministic(t *testing.T) {
	mac1 := generateENIMac("eni-abc123")
	mac2 := generateENIMac("eni-abc123")
	assert.Equal(t, mac1, mac2, "same input must produce same MAC")
}

func TestGenerateENIMac_DifferentInputs(t *testing.T) {
	mac1 := generateENIMac("eni-aaa")
	mac2 := generateENIMac("eni-bbb")
	assert.NotEqual(t, mac1, mac2, "different inputs should produce different MACs")
}

func TestGenerateENIMac_LocallyAdministered(t *testing.T) {
	mac := generateENIMac("eni-test123")
	// Must start with 02:00:00 (locally-administered unicast)
	assert.Regexp(t, regexp.MustCompile(`^02:00:00:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}$`), mac)
}

func TestGenerateENIMac_EmptyString(t *testing.T) {
	mac := generateENIMac("")
	// Hash of empty string = 0, so all three octets should be 00
	assert.Equal(t, "02:00:00:00:00:00", mac)
}
