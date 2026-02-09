package handlers_ec2_snapshot

import (
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// NATSSnapshotService handles snapshot operations via NATS messaging
type NATSSnapshotService struct {
	natsConn *nats.Conn
}

// NewNATSSnapshotService creates a new NATS-based snapshot service
func NewNATSSnapshotService(conn *nats.Conn) SnapshotService {
	return &NATSSnapshotService{natsConn: conn}
}

func (s *NATSSnapshotService) CreateSnapshot(input *ec2.CreateSnapshotInput) (*ec2.Snapshot, error) {
	return utils.NATSRequest[ec2.Snapshot](s.natsConn, "ec2.CreateSnapshot", input, 120*time.Second)
}

func (s *NATSSnapshotService) DescribeSnapshots(input *ec2.DescribeSnapshotsInput) (*ec2.DescribeSnapshotsOutput, error) {
	return utils.NATSRequest[ec2.DescribeSnapshotsOutput](s.natsConn, "ec2.DescribeSnapshots", input, 30*time.Second)
}

func (s *NATSSnapshotService) DeleteSnapshot(input *ec2.DeleteSnapshotInput) (*ec2.DeleteSnapshotOutput, error) {
	return utils.NATSRequest[ec2.DeleteSnapshotOutput](s.natsConn, "ec2.DeleteSnapshot", input, 60*time.Second)
}

func (s *NATSSnapshotService) CopySnapshot(input *ec2.CopySnapshotInput) (*ec2.CopySnapshotOutput, error) {
	return utils.NATSRequest[ec2.CopySnapshotOutput](s.natsConn, "ec2.CopySnapshot", input, 120*time.Second)
}
