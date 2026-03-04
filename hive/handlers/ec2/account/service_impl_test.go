package handlers_ec2_account

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewAccountSettingsServiceImplWithNATS_NoJetStream verifies that creating
// the service against a NATS server without JetStream returns an error instead
// of silently degrading.
func TestNewAccountSettingsServiceImplWithNATS_NoJetStream(t *testing.T) {
	ns, err := server.NewServer(&server.Options{
		Host:   "127.0.0.1",
		Port:   -1,
		NoLog:  true,
		NoSigs: true,
	})
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close() })

	svc, err := NewAccountSettingsServiceImplWithNATS(nil, nc)
	assert.Error(t, err)
	assert.Nil(t, svc)
}

var _ AccountSettingsService = (*AccountSettingsServiceImpl)(nil)

func setupTestAccountService(t *testing.T) *AccountSettingsServiceImpl {
	t.Helper()
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close() })

	svc, err := NewAccountSettingsServiceImplWithNATS(nil, nc)
	require.NoError(t, err)
	return svc
}

const testAccountID = "111111111111"

// EBS Encryption tests

func TestEbsEncryption_DefaultOff(t *testing.T) {
	svc := setupTestAccountService(t)
	out, err := svc.GetEbsEncryptionByDefault(&ec2.GetEbsEncryptionByDefaultInput{}, testAccountID)
	require.NoError(t, err)
	assert.False(t, *out.EbsEncryptionByDefault)
}

func TestEbsEncryption_EnableDisable(t *testing.T) {
	svc := setupTestAccountService(t)

	// Enable
	enableOut, err := svc.EnableEbsEncryptionByDefault(&ec2.EnableEbsEncryptionByDefaultInput{}, testAccountID)
	require.NoError(t, err)
	assert.True(t, *enableOut.EbsEncryptionByDefault)

	// Verify
	getOut, err := svc.GetEbsEncryptionByDefault(&ec2.GetEbsEncryptionByDefaultInput{}, testAccountID)
	require.NoError(t, err)
	assert.True(t, *getOut.EbsEncryptionByDefault)

	// Disable
	disableOut, err := svc.DisableEbsEncryptionByDefault(&ec2.DisableEbsEncryptionByDefaultInput{}, testAccountID)
	require.NoError(t, err)
	assert.False(t, *disableOut.EbsEncryptionByDefault)

	// Verify
	getOut, err = svc.GetEbsEncryptionByDefault(&ec2.GetEbsEncryptionByDefaultInput{}, testAccountID)
	require.NoError(t, err)
	assert.False(t, *getOut.EbsEncryptionByDefault)
}

// Serial Console tests

func TestSerialConsole_DefaultOff(t *testing.T) {
	svc := setupTestAccountService(t)
	out, err := svc.GetSerialConsoleAccessStatus(&ec2.GetSerialConsoleAccessStatusInput{}, testAccountID)
	require.NoError(t, err)
	assert.False(t, *out.SerialConsoleAccessEnabled)
}

func TestSerialConsole_EnableDisable(t *testing.T) {
	svc := setupTestAccountService(t)

	enableOut, err := svc.EnableSerialConsoleAccess(&ec2.EnableSerialConsoleAccessInput{}, testAccountID)
	require.NoError(t, err)
	assert.True(t, *enableOut.SerialConsoleAccessEnabled)

	getOut, err := svc.GetSerialConsoleAccessStatus(&ec2.GetSerialConsoleAccessStatusInput{}, testAccountID)
	require.NoError(t, err)
	assert.True(t, *getOut.SerialConsoleAccessEnabled)

	disableOut, err := svc.DisableSerialConsoleAccess(&ec2.DisableSerialConsoleAccessInput{}, testAccountID)
	require.NoError(t, err)
	assert.False(t, *disableOut.SerialConsoleAccessEnabled)

	getOut, err = svc.GetSerialConsoleAccessStatus(&ec2.GetSerialConsoleAccessStatusInput{}, testAccountID)
	require.NoError(t, err)
	assert.False(t, *getOut.SerialConsoleAccessEnabled)
}

// Multi-account isolation tests

func TestEbsEncryption_MultiAccountIsolation(t *testing.T) {
	svc := setupTestAccountService(t)
	accountA := "111111111111"
	accountB := "222222222222"

	// Enable EBS encryption for account A only
	_, err := svc.EnableEbsEncryptionByDefault(&ec2.EnableEbsEncryptionByDefaultInput{}, accountA)
	require.NoError(t, err)

	// Account A should have encryption enabled
	outA, err := svc.GetEbsEncryptionByDefault(&ec2.GetEbsEncryptionByDefaultInput{}, accountA)
	require.NoError(t, err)
	assert.True(t, *outA.EbsEncryptionByDefault)

	// Account B should still have encryption disabled (default)
	outB, err := svc.GetEbsEncryptionByDefault(&ec2.GetEbsEncryptionByDefaultInput{}, accountB)
	require.NoError(t, err)
	assert.False(t, *outB.EbsEncryptionByDefault)
}

func TestSerialConsole_MultiAccountIsolation(t *testing.T) {
	svc := setupTestAccountService(t)
	accountA := "111111111111"
	accountB := "222222222222"

	// Enable serial console for account A only
	_, err := svc.EnableSerialConsoleAccess(&ec2.EnableSerialConsoleAccessInput{}, accountA)
	require.NoError(t, err)

	// Account A should have serial console enabled
	outA, err := svc.GetSerialConsoleAccessStatus(&ec2.GetSerialConsoleAccessStatusInput{}, accountA)
	require.NoError(t, err)
	assert.True(t, *outA.SerialConsoleAccessEnabled)

	// Account B should still have serial console disabled (default)
	outB, err := svc.GetSerialConsoleAccessStatus(&ec2.GetSerialConsoleAccessStatusInput{}, accountB)
	require.NoError(t, err)
	assert.False(t, *outB.SerialConsoleAccessEnabled)
}

func TestSettingsKey_EmptyAccountIDFallback(t *testing.T) {
	assert.Equal(t, "default", settingsKey(""))
	assert.Equal(t, "123456789012", settingsKey("123456789012"))
}
