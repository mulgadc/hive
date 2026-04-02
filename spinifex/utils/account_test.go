package utils

import (
	"testing"

	"github.com/mulgadc/spinifex/spinifex/testutil"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountKey(t *testing.T) {
	tests := []struct {
		accountID  string
		resourceID string
		want       string
	}{
		{"000000000000", "vpc-123", "000000000000.vpc-123"},
		{"123456789012", "igw-abc", "123456789012.igw-abc"},
		{"", "vol-1", ".vol-1"},
	}
	for _, tt := range tests {
		got := AccountKey(tt.accountID, tt.resourceID)
		if got != tt.want {
			t.Errorf("AccountKey(%q, %q) = %q, want %q", tt.accountID, tt.resourceID, got, tt.want)
		}
	}
}

func TestIsAccountID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"000000000000", true},
		{"123456789012", true},
		{"999999999999", true},
		{"self", false},
		{"spinifex", false},
		{"", false},
		{"12345678901", false},   // 11 digits
		{"1234567890123", false}, // 13 digits
		{"12345678901a", false},  // non-digit
		{"abcdefghijkl", false},
	}
	for _, tt := range tests {
		got := IsAccountID(tt.input)
		if got != tt.want {
			t.Errorf("IsAccountID(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestGlobalAccountID(t *testing.T) {
	if GlobalAccountID != "000000000000" {
		t.Errorf("GlobalAccountID = %q, want %q", GlobalAccountID, "000000000000")
	}
	if !IsAccountID(GlobalAccountID) {
		t.Error("GlobalAccountID should be a valid account ID")
	}
}

// startJSNATSServer starts an embedded JetStream-enabled NATS server for testing.
func startJSNATSServer(t *testing.T) (*nats.Conn, nats.JetStreamContext) {
	t.Helper()
	_, nc, js := testutil.StartTestJetStream(t)
	return nc, js
}

func TestWriteVersion_WritesKey(t *testing.T) {
	_, js := startJSNATSServer(t)

	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: "test-write-version"})
	require.NoError(t, err)

	err = WriteVersion(kv, 1)
	require.NoError(t, err)

	entry, err := kv.Get(VersionKey)
	require.NoError(t, err)
	assert.Equal(t, "1", string(entry.Value()))
}

func TestReadVersion_ReturnsZeroWhenUnset(t *testing.T) {
	_, js := startJSNATSServer(t)

	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: "test-read-unset"})
	require.NoError(t, err)

	v, err := ReadVersion(kv)
	require.NoError(t, err)
	assert.Equal(t, 0, v)
}

func TestWriteVersion_IdempotentOnSameVersion(t *testing.T) {
	_, js := startJSNATSServer(t)

	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: "test-idempotent"})
	require.NoError(t, err)

	require.NoError(t, WriteVersion(kv, 1))
	require.NoError(t, WriteVersion(kv, 1))

	v, err := ReadVersion(kv)
	require.NoError(t, err)
	assert.Equal(t, 1, v)
}

func TestWriteVersion_UpdatesOnHigherVersion(t *testing.T) {
	_, js := startJSNATSServer(t)

	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: "test-upgrade"})
	require.NoError(t, err)

	require.NoError(t, WriteVersion(kv, 1))
	require.NoError(t, WriteVersion(kv, 2))

	v, err := ReadVersion(kv)
	require.NoError(t, err)
	assert.Equal(t, 2, v)
}

func TestWriteVersion_NoOpOnLowerVersion(t *testing.T) {
	_, js := startJSNATSServer(t)

	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: "test-downgrade"})
	require.NoError(t, err)

	require.NoError(t, WriteVersion(kv, 3))
	require.NoError(t, WriteVersion(kv, 1))

	v, err := ReadVersion(kv)
	require.NoError(t, err)
	assert.Equal(t, 3, v)
}
