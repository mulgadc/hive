package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/mulgadc/spinifex/spinifex/formation"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/stretchr/testify/assert"
)

// Regression guard: source URL must print for every error kind, not just
// mismatch, so a 404/non-HTTPS/size-cap failure still tells the operator
// which URL to investigate.
func TestPrintChecksumError(t *testing.T) {
	image := utils.Images{Checksum: "https://example.com/SUMS", ChecksumType: "sha512"}
	const imageFile = "/var/lib/img.tar.xz"
	const imageName = "debian-12-x86_64"

	errs := []error{
		fmt.Errorf("%w: expected abc got def", utils.ErrChecksumMismatch),
		fmt.Errorf("%w: 404", utils.ErrChecksumFetchFailed),
		errors.New("open /x: no such file"),
	}
	for _, e := range errs {
		var buf bytes.Buffer
		printChecksumError(&buf, imageFile, imageName, image, e)
		out := buf.String()
		assert.Contains(t, out, image.Checksum, "source URL must print for: %v", e)
		assert.Contains(t, out, imageFile)
		assert.Contains(t, out, "spx admin images import --name "+imageName+" --force")
	}
}

// buildRemoteNodes must prefer AdvertiseIP (off-host dial target) and fall
// back to BindIP when the peer pre-dates siv-8 and didn't send AdvertiseIP.
func TestBuildRemoteNodes_AdvertiseFallback(t *testing.T) {
	nodes := map[string]formation.NodeInfo{
		"node1": {Name: "node1", BindIP: "10.0.0.1", AdvertiseIP: "203.0.113.1"},
		"node2": {Name: "node2", BindIP: "10.0.0.2"}, // legacy joiner
		"node3": {Name: "node3", BindIP: "10.0.0.3", AdvertiseIP: "203.0.113.3"},
	}
	got := buildRemoteNodes(nodes, "node3")
	if assert.Len(t, got, 2) {
		assert.Equal(t, "node1", got[0].Name)
		assert.Equal(t, "203.0.113.1", got[0].Host, "advertise wins when set")
		assert.Equal(t, "node2", got[1].Name)
		assert.Equal(t, "10.0.0.2", got[1].Host, "bind fallback when advertise empty")
	}
}
