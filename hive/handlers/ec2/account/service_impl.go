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

