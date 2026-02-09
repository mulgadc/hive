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

func (s *NATSAccountSettingsService) GetInstanceMetadataDefaults(input *ec2.GetInstanceMetadataDefaultsInput) (*ec2.GetInstanceMetadataDefaultsOutput, error) {
	return utils.NATSRequest[ec2.GetInstanceMetadataDefaultsOutput](s.natsConn, "ec2.GetInstanceMetadataDefaults", input, 30*time.Second)
}

func (s *NATSAccountSettingsService) ModifyInstanceMetadataDefaults(input *ec2.ModifyInstanceMetadataDefaultsInput) (*ec2.ModifyInstanceMetadataDefaultsOutput, error) {
	return utils.NATSRequest[ec2.ModifyInstanceMetadataDefaultsOutput](s.natsConn, "ec2.ModifyInstanceMetadataDefaults", input, 30*time.Second)
}

func (s *NATSAccountSettingsService) GetSnapshotBlockPublicAccessState(input *ec2.GetSnapshotBlockPublicAccessStateInput) (*ec2.GetSnapshotBlockPublicAccessStateOutput, error) {
	return utils.NATSRequest[ec2.GetSnapshotBlockPublicAccessStateOutput](s.natsConn, "ec2.GetSnapshotBlockPublicAccessState", input, 30*time.Second)
}

func (s *NATSAccountSettingsService) EnableSnapshotBlockPublicAccess(input *ec2.EnableSnapshotBlockPublicAccessInput) (*ec2.EnableSnapshotBlockPublicAccessOutput, error) {
	return utils.NATSRequest[ec2.EnableSnapshotBlockPublicAccessOutput](s.natsConn, "ec2.EnableSnapshotBlockPublicAccess", input, 30*time.Second)
}

func (s *NATSAccountSettingsService) DisableSnapshotBlockPublicAccess(input *ec2.DisableSnapshotBlockPublicAccessInput) (*ec2.DisableSnapshotBlockPublicAccessOutput, error) {
	return utils.NATSRequest[ec2.DisableSnapshotBlockPublicAccessOutput](s.natsConn, "ec2.DisableSnapshotBlockPublicAccess", input, 30*time.Second)
}

func (s *NATSAccountSettingsService) GetImageBlockPublicAccessState(input *ec2.GetImageBlockPublicAccessStateInput) (*ec2.GetImageBlockPublicAccessStateOutput, error) {
	return utils.NATSRequest[ec2.GetImageBlockPublicAccessStateOutput](s.natsConn, "ec2.GetImageBlockPublicAccessState", input, 30*time.Second)
}

func (s *NATSAccountSettingsService) EnableImageBlockPublicAccess(input *ec2.EnableImageBlockPublicAccessInput) (*ec2.EnableImageBlockPublicAccessOutput, error) {
	return utils.NATSRequest[ec2.EnableImageBlockPublicAccessOutput](s.natsConn, "ec2.EnableImageBlockPublicAccess", input, 30*time.Second)
}

func (s *NATSAccountSettingsService) DisableImageBlockPublicAccess(input *ec2.DisableImageBlockPublicAccessInput) (*ec2.DisableImageBlockPublicAccessOutput, error) {
	return utils.NATSRequest[ec2.DisableImageBlockPublicAccessOutput](s.natsConn, "ec2.DisableImageBlockPublicAccess", input, 30*time.Second)
}
