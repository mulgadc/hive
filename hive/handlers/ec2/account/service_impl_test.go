package handlers_ec2_account

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
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

// IMDS tests

func TestGetInstanceMetadataDefaults(t *testing.T) {
	svc := setupTestAccountService(t)
	out, err := svc.GetInstanceMetadataDefaults(&ec2.GetInstanceMetadataDefaultsInput{})
	require.NoError(t, err)
	require.NotNil(t, out.AccountLevel)
	assert.Equal(t, "optional", *out.AccountLevel.HttpTokens)
	assert.Equal(t, int64(1), *out.AccountLevel.HttpPutResponseHopLimit)
	assert.Equal(t, "enabled", *out.AccountLevel.HttpEndpoint)
}

func TestModifyInstanceMetadataDefaults(t *testing.T) {
	svc := setupTestAccountService(t)
	out, err := svc.ModifyInstanceMetadataDefaults(&ec2.ModifyInstanceMetadataDefaultsInput{
		HttpTokens: aws.String("required"),
	})
	require.NoError(t, err)
	assert.True(t, *out.Return)
}

// Snapshot Block Public Access tests

func TestGetSnapshotBlockPublicAccessState(t *testing.T) {
	svc := setupTestAccountService(t)
	out, err := svc.GetSnapshotBlockPublicAccessState(&ec2.GetSnapshotBlockPublicAccessStateInput{})
	require.NoError(t, err)
	assert.Equal(t, "block-all-sharing", *out.State)
}

func TestEnableSnapshotBlockPublicAccess(t *testing.T) {
	svc := setupTestAccountService(t)
	out, err := svc.EnableSnapshotBlockPublicAccess(&ec2.EnableSnapshotBlockPublicAccessInput{
		State: aws.String("block-all-sharing"),
	})
	require.NoError(t, err)
	assert.Equal(t, "block-all-sharing", *out.State)
}

func TestDisableSnapshotBlockPublicAccess(t *testing.T) {
	svc := setupTestAccountService(t)
	out, err := svc.DisableSnapshotBlockPublicAccess(&ec2.DisableSnapshotBlockPublicAccessInput{})
	require.NoError(t, err)
	assert.Equal(t, "unblocked", *out.State)
}

// Image Block Public Access tests

func TestGetImageBlockPublicAccessState(t *testing.T) {
	svc := setupTestAccountService(t)
	out, err := svc.GetImageBlockPublicAccessState(&ec2.GetImageBlockPublicAccessStateInput{})
	require.NoError(t, err)
	assert.Equal(t, "block-new-sharing", *out.ImageBlockPublicAccessState)
}

func TestEnableImageBlockPublicAccess(t *testing.T) {
	svc := setupTestAccountService(t)
	out, err := svc.EnableImageBlockPublicAccess(&ec2.EnableImageBlockPublicAccessInput{
		ImageBlockPublicAccessState: aws.String("block-new-sharing"),
	})
	require.NoError(t, err)
	assert.Equal(t, "block-new-sharing", *out.ImageBlockPublicAccessState)
}

func TestDisableImageBlockPublicAccess(t *testing.T) {
	svc := setupTestAccountService(t)
	out, err := svc.DisableImageBlockPublicAccess(&ec2.DisableImageBlockPublicAccessInput{})
	require.NoError(t, err)
	assert.Equal(t, "unblocked", *out.ImageBlockPublicAccessState)
}
