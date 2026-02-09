package handlers_ec2_account

import "github.com/aws/aws-sdk-go/service/ec2"

// AccountSettingsService defines the interface for EC2 account-level settings
type AccountSettingsService interface {
	EnableEbsEncryptionByDefault(input *ec2.EnableEbsEncryptionByDefaultInput) (*ec2.EnableEbsEncryptionByDefaultOutput, error)
	DisableEbsEncryptionByDefault(input *ec2.DisableEbsEncryptionByDefaultInput) (*ec2.DisableEbsEncryptionByDefaultOutput, error)
	GetEbsEncryptionByDefault(input *ec2.GetEbsEncryptionByDefaultInput) (*ec2.GetEbsEncryptionByDefaultOutput, error)
	GetSerialConsoleAccessStatus(input *ec2.GetSerialConsoleAccessStatusInput) (*ec2.GetSerialConsoleAccessStatusOutput, error)
	EnableSerialConsoleAccess(input *ec2.EnableSerialConsoleAccessInput) (*ec2.EnableSerialConsoleAccessOutput, error)
	DisableSerialConsoleAccess(input *ec2.DisableSerialConsoleAccessInput) (*ec2.DisableSerialConsoleAccessOutput, error)
	GetInstanceMetadataDefaults(input *ec2.GetInstanceMetadataDefaultsInput) (*ec2.GetInstanceMetadataDefaultsOutput, error)
	ModifyInstanceMetadataDefaults(input *ec2.ModifyInstanceMetadataDefaultsInput) (*ec2.ModifyInstanceMetadataDefaultsOutput, error)
	GetSnapshotBlockPublicAccessState(input *ec2.GetSnapshotBlockPublicAccessStateInput) (*ec2.GetSnapshotBlockPublicAccessStateOutput, error)
	EnableSnapshotBlockPublicAccess(input *ec2.EnableSnapshotBlockPublicAccessInput) (*ec2.EnableSnapshotBlockPublicAccessOutput, error)
	DisableSnapshotBlockPublicAccess(input *ec2.DisableSnapshotBlockPublicAccessInput) (*ec2.DisableSnapshotBlockPublicAccessOutput, error)
	GetImageBlockPublicAccessState(input *ec2.GetImageBlockPublicAccessStateInput) (*ec2.GetImageBlockPublicAccessStateOutput, error)
	EnableImageBlockPublicAccess(input *ec2.EnableImageBlockPublicAccessInput) (*ec2.EnableImageBlockPublicAccessOutput, error)
	DisableImageBlockPublicAccess(input *ec2.DisableImageBlockPublicAccessInput) (*ec2.DisableImageBlockPublicAccessOutput, error)
}
