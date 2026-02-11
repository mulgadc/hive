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
}
