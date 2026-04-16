package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

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
