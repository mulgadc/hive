package handlers_ec2_volume

import (
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// NATSVolumeService handles volume operations via NATS messaging
type NATSVolumeService struct {
	natsConn *nats.Conn
}

// NewNATSVolumeService creates a new NATS-based volume service
func NewNATSVolumeService(conn *nats.Conn) VolumeService {
	return &NATSVolumeService{natsConn: conn}
}

func (s *NATSVolumeService) CreateVolume(input *ec2.CreateVolumeInput) (*ec2.Volume, error) {
	return utils.NATSRequest[ec2.Volume](s.natsConn, "ec2.CreateVolume", input, 30*time.Second)
}

func (s *NATSVolumeService) DescribeVolumes(input *ec2.DescribeVolumesInput) (*ec2.DescribeVolumesOutput, error) {
	return utils.NATSRequest[ec2.DescribeVolumesOutput](s.natsConn, "ec2.DescribeVolumes", input, 30*time.Second)
}

func (s *NATSVolumeService) ModifyVolume(input *ec2.ModifyVolumeInput) (*ec2.ModifyVolumeOutput, error) {
	return utils.NATSRequest[ec2.ModifyVolumeOutput](s.natsConn, "ec2.ModifyVolume", input, 30*time.Second)
}

func (s *NATSVolumeService) DescribeVolumeStatus(input *ec2.DescribeVolumeStatusInput) (*ec2.DescribeVolumeStatusOutput, error) {
	return utils.NATSRequest[ec2.DescribeVolumeStatusOutput](s.natsConn, "ec2.DescribeVolumeStatus", input, 30*time.Second)
}

func (s *NATSVolumeService) DeleteVolume(input *ec2.DeleteVolumeInput) (*ec2.DeleteVolumeOutput, error) {
	return utils.NATSRequest[ec2.DeleteVolumeOutput](s.natsConn, "ec2.DeleteVolume", input, 30*time.Second)
}
