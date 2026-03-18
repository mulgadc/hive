package handlers_ec2_account

import "github.com/aws/aws-sdk-go/service/ec2"

// AccountSettingsService defines the interface for EC2 account-level settings
type AccountSettingsService interface {
	EnableEbsEncryptionByDefault(input *ec2.EnableEbsEncryptionByDefaultInput, accountID string) (*ec2.EnableEbsEncryptionByDefaultOutput, error)
	DisableEbsEncryptionByDefault(input *ec2.DisableEbsEncryptionByDefaultInput, accountID string) (*ec2.DisableEbsEncryptionByDefaultOutput, error)
	GetEbsEncryptionByDefault(input *ec2.GetEbsEncryptionByDefaultInput, accountID string) (*ec2.GetEbsEncryptionByDefaultOutput, error)
	GetSerialConsoleAccessStatus(input *ec2.GetSerialConsoleAccessStatusInput, accountID string) (*ec2.GetSerialConsoleAccessStatusOutput, error)
	EnableSerialConsoleAccess(input *ec2.EnableSerialConsoleAccessInput, accountID string) (*ec2.EnableSerialConsoleAccessOutput, error)
	DisableSerialConsoleAccess(input *ec2.DisableSerialConsoleAccessInput, accountID string) (*ec2.DisableSerialConsoleAccessOutput, error)
}
