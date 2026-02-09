package handlers_ec2_snapshot

import "github.com/aws/aws-sdk-go/service/ec2"

// SnapshotService defines the interface for EC2 snapshot operations
type SnapshotService interface {
	CreateSnapshot(input *ec2.CreateSnapshotInput) (*ec2.Snapshot, error)
	DescribeSnapshots(input *ec2.DescribeSnapshotsInput) (*ec2.DescribeSnapshotsOutput, error)
	DeleteSnapshot(input *ec2.DeleteSnapshotInput) (*ec2.DeleteSnapshotOutput, error)
	CopySnapshot(input *ec2.CopySnapshotInput) (*ec2.CopySnapshotOutput, error)
}
