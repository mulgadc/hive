package handlers_ec2_account

import (
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// NATSAccountSettingsService implements AccountSettingsService via NATS messaging
type NATSAccountSettingsService struct {
	natsConn *nats.Conn
}

// NewNATSAccountSettingsService creates a new NATS-based account settings service
func NewNATSAccountSettingsService(natsConn *nats.Conn) AccountSettingsService {
	return &NATSAccountSettingsService{natsConn: natsConn}
}

func (s *NATSAccountSettingsService) EnableEbsEncryptionByDefault(input *ec2.EnableEbsEncryptionByDefaultInput) (*ec2.EnableEbsEncryptionByDefaultOutput, error) {
	return utils.NATSRequest[ec2.EnableEbsEncryptionByDefaultOutput](s.natsConn, "ec2.EnableEbsEncryptionByDefault", input, 30*time.Second)
}

func (s *NATSAccountSettingsService) DisableEbsEncryptionByDefault(input *ec2.DisableEbsEncryptionByDefaultInput) (*ec2.DisableEbsEncryptionByDefaultOutput, error) {
	return utils.NATSRequest[ec2.DisableEbsEncryptionByDefaultOutput](s.natsConn, "ec2.DisableEbsEncryptionByDefault", input, 30*time.Second)
}

func (s *NATSAccountSettingsService) GetEbsEncryptionByDefault(input *ec2.GetEbsEncryptionByDefaultInput) (*ec2.GetEbsEncryptionByDefaultOutput, error) {
	return utils.NATSRequest[ec2.GetEbsEncryptionByDefaultOutput](s.natsConn, "ec2.GetEbsEncryptionByDefault", input, 30*time.Second)
}

func (s *NATSAccountSettingsService) GetSerialConsoleAccessStatus(input *ec2.GetSerialConsoleAccessStatusInput) (*ec2.GetSerialConsoleAccessStatusOutput, error) {
	return utils.NATSRequest[ec2.GetSerialConsoleAccessStatusOutput](s.natsConn, "ec2.GetSerialConsoleAccessStatus", input, 30*time.Second)
}

func (s *NATSAccountSettingsService) EnableSerialConsoleAccess(input *ec2.EnableSerialConsoleAccessInput) (*ec2.EnableSerialConsoleAccessOutput, error) {
	return utils.NATSRequest[ec2.EnableSerialConsoleAccessOutput](s.natsConn, "ec2.EnableSerialConsoleAccess", input, 30*time.Second)
}

func (s *NATSAccountSettingsService) DisableSerialConsoleAccess(input *ec2.DisableSerialConsoleAccessInput) (*ec2.DisableSerialConsoleAccessOutput, error) {
	return utils.NATSRequest[ec2.DisableSerialConsoleAccessOutput](s.natsConn, "ec2.DisableSerialConsoleAccess", input, 30*time.Second)
}

