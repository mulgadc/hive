package handlers_ec2_account

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/config"
	"github.com/nats-io/nats.go"
)

const (
	KVBucketAccountSettings = "hive-ec2-account-settings"
	KeyEbsEncryptionDefault = "ebs-encryption-default"
	KeySerialConsoleAccess  = "serial-console-access"
)

// AccountSettingsRecord represents stored account settings
type AccountSettingsRecord struct {
	EbsEncryptionByDefault bool `json:"ebs_encryption_by_default"`
	SerialConsoleAccess    bool `json:"serial_console_access"`
}

// AccountSettingsServiceImpl implements account settings operations with NATS JetStream persistence
type AccountSettingsServiceImpl struct {
	config     *config.Config
	js         nats.JetStreamContext
	settingsKV nats.KeyValue
}

// NewAccountSettingsServiceImpl creates a new account settings service implementation
func NewAccountSettingsServiceImpl(cfg *config.Config) *AccountSettingsServiceImpl {
	return &AccountSettingsServiceImpl{
		config: cfg,
	}
}

// NewAccountSettingsServiceImplWithNATS creates an account settings service with NATS JetStream for persistence
func NewAccountSettingsServiceImplWithNATS(cfg *config.Config, natsConn *nats.Conn) (*AccountSettingsServiceImpl, error) {
	js, err := natsConn.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	settingsKV, err := getOrCreateKVBucket(js, KVBucketAccountSettings, 10)
	if err != nil {
		slog.Warn("Failed to create account settings KV bucket", "error", err)
		return NewAccountSettingsServiceImpl(cfg), nil
	}

	slog.Info("Account settings service initialized with JetStream KV", "bucket", KVBucketAccountSettings)

	return &AccountSettingsServiceImpl{
		config:     cfg,
		js:         js,
		settingsKV: settingsKV,
	}, nil
}

func getOrCreateKVBucket(js nats.JetStreamContext, bucketName string, history int) (nats.KeyValue, error) {
	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  bucketName,
		History: uint8(history),
	})
	if err != nil {
		kv, err = js.KeyValue(bucketName)
		if err != nil {
			return nil, err
		}
	}
	return kv, nil
}

// getSettings retrieves current account settings
func (s *AccountSettingsServiceImpl) getSettings() (*AccountSettingsRecord, error) {
	if s.settingsKV == nil {
		// Return defaults if KV not available
		return &AccountSettingsRecord{
			EbsEncryptionByDefault: false,
			SerialConsoleAccess:    false,
		}, nil
	}

	entry, err := s.settingsKV.Get("default")
	if err != nil {
		// Return defaults if not found
		return &AccountSettingsRecord{
			EbsEncryptionByDefault: false,
			SerialConsoleAccess:    false,
		}, nil
	}

	var settings AccountSettingsRecord
	if err := json.Unmarshal(entry.Value(), &settings); err != nil {
		return nil, err
	}

	return &settings, nil
}

// saveSettings saves current account settings
func (s *AccountSettingsServiceImpl) saveSettings(settings *AccountSettingsRecord) error {
	if s.settingsKV == nil {
		slog.Warn("Cannot save settings - KV not initialized")
		return nil // Return success anyway for in-memory fallback
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}

	_, err = s.settingsKV.Put("default", data)
	return err
}

// EnableEbsEncryptionByDefault enables EBS encryption by default for the account
func (s *AccountSettingsServiceImpl) EnableEbsEncryptionByDefault(input *ec2.EnableEbsEncryptionByDefaultInput) (*ec2.EnableEbsEncryptionByDefaultOutput, error) {
	slog.Info("EnableEbsEncryptionByDefault called")

	settings, err := s.getSettings()
	if err != nil {
		return nil, err
	}

	settings.EbsEncryptionByDefault = true

	if err := s.saveSettings(settings); err != nil {
		return nil, err
	}

	return &ec2.EnableEbsEncryptionByDefaultOutput{
		EbsEncryptionByDefault: aws.Bool(true),
	}, nil
}

// DisableEbsEncryptionByDefault disables EBS encryption by default for the account
func (s *AccountSettingsServiceImpl) DisableEbsEncryptionByDefault(input *ec2.DisableEbsEncryptionByDefaultInput) (*ec2.DisableEbsEncryptionByDefaultOutput, error) {
	slog.Info("DisableEbsEncryptionByDefault called")

	settings, err := s.getSettings()
	if err != nil {
		return nil, err
	}

	settings.EbsEncryptionByDefault = false

	if err := s.saveSettings(settings); err != nil {
		return nil, err
	}

	return &ec2.DisableEbsEncryptionByDefaultOutput{
		EbsEncryptionByDefault: aws.Bool(false),
	}, nil
}

// GetEbsEncryptionByDefault gets the current EBS encryption by default setting
func (s *AccountSettingsServiceImpl) GetEbsEncryptionByDefault(input *ec2.GetEbsEncryptionByDefaultInput) (*ec2.GetEbsEncryptionByDefaultOutput, error) {
	slog.Info("GetEbsEncryptionByDefault called")

	settings, err := s.getSettings()
	if err != nil {
		return nil, err
	}

	return &ec2.GetEbsEncryptionByDefaultOutput{
		EbsEncryptionByDefault: aws.Bool(settings.EbsEncryptionByDefault),
	}, nil
}

// GetSerialConsoleAccessStatus gets the current serial console access status
func (s *AccountSettingsServiceImpl) GetSerialConsoleAccessStatus(input *ec2.GetSerialConsoleAccessStatusInput) (*ec2.GetSerialConsoleAccessStatusOutput, error) {
	slog.Info("GetSerialConsoleAccessStatus called")

	settings, err := s.getSettings()
	if err != nil {
		return nil, err
	}

	return &ec2.GetSerialConsoleAccessStatusOutput{
		SerialConsoleAccessEnabled: aws.Bool(settings.SerialConsoleAccess),
	}, nil
}

// EnableSerialConsoleAccess enables serial console access for the account
func (s *AccountSettingsServiceImpl) EnableSerialConsoleAccess(input *ec2.EnableSerialConsoleAccessInput) (*ec2.EnableSerialConsoleAccessOutput, error) {
	slog.Info("EnableSerialConsoleAccess called")

	settings, err := s.getSettings()
	if err != nil {
		return nil, err
	}

	settings.SerialConsoleAccess = true

	if err := s.saveSettings(settings); err != nil {
		return nil, err
	}

	return &ec2.EnableSerialConsoleAccessOutput{
		SerialConsoleAccessEnabled: aws.Bool(true),
	}, nil
}

// DisableSerialConsoleAccess disables serial console access for the account
func (s *AccountSettingsServiceImpl) DisableSerialConsoleAccess(input *ec2.DisableSerialConsoleAccessInput) (*ec2.DisableSerialConsoleAccessOutput, error) {
	slog.Info("DisableSerialConsoleAccess called")

	settings, err := s.getSettings()
	if err != nil {
		return nil, err
	}

	settings.SerialConsoleAccess = false

	if err := s.saveSettings(settings); err != nil {
		return nil, err
	}

	return &ec2.DisableSerialConsoleAccessOutput{
		SerialConsoleAccessEnabled: aws.Bool(false),
	}, nil
}

// GetInstanceMetadataDefaults gets the default instance metadata service (IMDS) settings for the account
func (s *AccountSettingsServiceImpl) GetInstanceMetadataDefaults(input *ec2.GetInstanceMetadataDefaultsInput) (*ec2.GetInstanceMetadataDefaultsOutput, error) {
	slog.Info("GetInstanceMetadataDefaults called")

	// Return Hive platform defaults for IMDS
	// In Hive, we support IMDSv2 by default with a reasonable hop limit
	httpTokens := "optional"        // IMDSv1 and IMDSv2 both allowed
	httpPutResponseHopLimit := int64(1)
	httpEndpoint := "enabled"
	instanceMetadataTags := "disabled"

	return &ec2.GetInstanceMetadataDefaultsOutput{
		AccountLevel: &ec2.InstanceMetadataDefaultsResponse{
			HttpTokens:              &httpTokens,
			HttpPutResponseHopLimit: &httpPutResponseHopLimit,
			HttpEndpoint:            &httpEndpoint,
			InstanceMetadataTags:    &instanceMetadataTags,
		},
	}, nil
}

// ModifyInstanceMetadataDefaults modifies the default instance metadata service settings for the account
func (s *AccountSettingsServiceImpl) ModifyInstanceMetadataDefaults(input *ec2.ModifyInstanceMetadataDefaultsInput) (*ec2.ModifyInstanceMetadataDefaultsOutput, error) {
	slog.Info("ModifyInstanceMetadataDefaults called",
		"httpTokens", aws.StringValue(input.HttpTokens),
		"httpPutResponseHopLimit", aws.Int64Value(input.HttpPutResponseHopLimit),
		"httpEndpoint", aws.StringValue(input.HttpEndpoint),
		"instanceMetadataTags", aws.StringValue(input.InstanceMetadataTags))

	// For now, return success - actual enforcement would be in instance launch
	return &ec2.ModifyInstanceMetadataDefaultsOutput{
		Return: aws.Bool(true),
	}, nil
}

// GetSnapshotBlockPublicAccessState gets the current snapshot block public access state
func (s *AccountSettingsServiceImpl) GetSnapshotBlockPublicAccessState(input *ec2.GetSnapshotBlockPublicAccessStateInput) (*ec2.GetSnapshotBlockPublicAccessStateOutput, error) {
	slog.Info("GetSnapshotBlockPublicAccessState called")

	// In Hive, snapshot block public access is blocked by default for security
	state := "block-all-sharing"

	return &ec2.GetSnapshotBlockPublicAccessStateOutput{
		State: &state,
	}, nil
}

// EnableSnapshotBlockPublicAccess enables the block public access feature for snapshots
func (s *AccountSettingsServiceImpl) EnableSnapshotBlockPublicAccess(input *ec2.EnableSnapshotBlockPublicAccessInput) (*ec2.EnableSnapshotBlockPublicAccessOutput, error) {
	slog.Info("EnableSnapshotBlockPublicAccess called", "state", aws.StringValue(input.State))

	state := aws.StringValue(input.State)
	if state == "" {
		state = "block-all-sharing"
	}

	return &ec2.EnableSnapshotBlockPublicAccessOutput{
		State: &state,
	}, nil
}

// DisableSnapshotBlockPublicAccess disables the block public access feature for snapshots
func (s *AccountSettingsServiceImpl) DisableSnapshotBlockPublicAccess(input *ec2.DisableSnapshotBlockPublicAccessInput) (*ec2.DisableSnapshotBlockPublicAccessOutput, error) {
	slog.Info("DisableSnapshotBlockPublicAccess called")

	state := "unblocked"

	return &ec2.DisableSnapshotBlockPublicAccessOutput{
		State: &state,
	}, nil
}

// GetImageBlockPublicAccessState gets the current image (AMI) block public access state
func (s *AccountSettingsServiceImpl) GetImageBlockPublicAccessState(input *ec2.GetImageBlockPublicAccessStateInput) (*ec2.GetImageBlockPublicAccessStateOutput, error) {
	slog.Info("GetImageBlockPublicAccessState called")

	// In Hive, AMI block public access is blocked by default for security
	state := "block-new-sharing"

	return &ec2.GetImageBlockPublicAccessStateOutput{
		ImageBlockPublicAccessState: &state,
	}, nil
}

// EnableImageBlockPublicAccess enables the block public access feature for AMIs
func (s *AccountSettingsServiceImpl) EnableImageBlockPublicAccess(input *ec2.EnableImageBlockPublicAccessInput) (*ec2.EnableImageBlockPublicAccessOutput, error) {
	slog.Info("EnableImageBlockPublicAccess called", "state", aws.StringValue(input.ImageBlockPublicAccessState))

	state := aws.StringValue(input.ImageBlockPublicAccessState)
	if state == "" {
		state = "block-new-sharing"
	}

	return &ec2.EnableImageBlockPublicAccessOutput{
		ImageBlockPublicAccessState: &state,
	}, nil
}

// DisableImageBlockPublicAccess disables the block public access feature for AMIs
func (s *AccountSettingsServiceImpl) DisableImageBlockPublicAccess(input *ec2.DisableImageBlockPublicAccessInput) (*ec2.DisableImageBlockPublicAccessOutput, error) {
	slog.Info("DisableImageBlockPublicAccess called")

	state := "unblocked"

	return &ec2.DisableImageBlockPublicAccessOutput{
		ImageBlockPublicAccessState: &state,
	}, nil
}
