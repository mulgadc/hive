package handlers_elbv2

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Verify NATSELBv2Service implements ELBv2Service at compile time.
var _ ELBv2Service = (*NATSELBv2Service)(nil)

func TestNewNATSELBv2Service_NilConn(t *testing.T) {
	// Should create the service even with nil conn (conn is used at call time, not construction)
	svc := NewNATSELBv2Service(nil)
	assert.NotNil(t, svc)
}
