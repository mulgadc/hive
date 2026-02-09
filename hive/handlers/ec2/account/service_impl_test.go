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

// EBS Encryption tests

func TestEbsEncryption_DefaultOff(t *testing.T) {
	svc := setupTestAccountService(t)
	out, err := svc.GetEbsEncryptionByDefault(&ec2.GetEbsEncryptionByDefaultInput{})
	require.NoError(t, err)
	assert.False(t, *out.EbsEncryptionByDefault)
}

func TestEbsEncryption_EnableDisable(t *testing.T) {
	svc := setupTestAccountService(t)

	// Enable
	enableOut, err := svc.EnableEbsEncryptionByDefault(&ec2.EnableEbsEncryptionByDefaultInput{})
	require.NoError(t, err)
	assert.True(t, *enableOut.EbsEncryptionByDefault)

	// Verify
	getOut, err := svc.GetEbsEncryptionByDefault(&ec2.GetEbsEncryptionByDefaultInput{})
	require.NoError(t, err)
	assert.True(t, *getOut.EbsEncryptionByDefault)

	// Disable
	disableOut, err := svc.DisableEbsEncryptionByDefault(&ec2.DisableEbsEncryptionByDefaultInput{})
	require.NoError(t, err)
	assert.False(t, *disableOut.EbsEncryptionByDefault)

	// Verify
	getOut, err = svc.GetEbsEncryptionByDefault(&ec2.GetEbsEncryptionByDefaultInput{})
	require.NoError(t, err)
	assert.False(t, *getOut.EbsEncryptionByDefault)
}

// Serial Console tests

func TestSerialConsole_DefaultOff(t *testing.T) {
	svc := setupTestAccountService(t)
	out, err := svc.GetSerialConsoleAccessStatus(&ec2.GetSerialConsoleAccessStatusInput{})
	require.NoError(t, err)
	assert.False(t, *out.SerialConsoleAccessEnabled)
}

func TestSerialConsole_EnableDisable(t *testing.T) {
	svc := setupTestAccountService(t)

	enableOut, err := svc.EnableSerialConsoleAccess(&ec2.EnableSerialConsoleAccessInput{})
	require.NoError(t, err)
	assert.True(t, *enableOut.SerialConsoleAccessEnabled)

	getOut, err := svc.GetSerialConsoleAccessStatus(&ec2.GetSerialConsoleAccessStatusInput{})
	require.NoError(t, err)
	assert.True(t, *getOut.SerialConsoleAccessEnabled)

	disableOut, err := svc.DisableSerialConsoleAccess(&ec2.DisableSerialConsoleAccessInput{})
	require.NoError(t, err)
	assert.False(t, *disableOut.SerialConsoleAccessEnabled)

	getOut, err = svc.GetSerialConsoleAccessStatus(&ec2.GetSerialConsoleAccessStatusInput{})
	require.NoError(t, err)
	assert.False(t, *getOut.SerialConsoleAccessEnabled)
}

