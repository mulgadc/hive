package handlers_ec2_volume

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/stretchr/testify/assert"
)

func newTestVolumeService(az string) *VolumeServiceImpl {
	return &VolumeServiceImpl{
		config: &config.Config{
			AZ: az,
			Predastore: config.PredastoreConfig{
				Bucket: "test-bucket",
				Region: "ap-southeast-2",
				Host:   "localhost:9000",
			},
			AccessKey: "testkey",
			SecretKey: "testsecret",
			WalDir:    "/tmp/test-wal",
		},
		// s3Client is nil - tests that hit S3/viperblock will fail,
		// which is expected for unit-level validation tests.
	}
}

func TestCreateVolume_NilInput(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	_, err := svc.CreateVolume(nil)
	assert.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestCreateVolume_InvalidSize_Zero(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	input := &ec2.CreateVolumeInput{
		Size:             aws.Int64(0),
		AvailabilityZone: aws.String("ap-southeast-2a"),
	}
	_, err := svc.CreateVolume(input)
	assert.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestCreateVolume_InvalidSize_Negative(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	input := &ec2.CreateVolumeInput{
		Size:             aws.Int64(-5),
		AvailabilityZone: aws.String("ap-southeast-2a"),
	}
	_, err := svc.CreateVolume(input)
	assert.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestCreateVolume_InvalidSize_TooLarge(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	input := &ec2.CreateVolumeInput{
		Size:             aws.Int64(16385),
		AvailabilityZone: aws.String("ap-southeast-2a"),
	}
	_, err := svc.CreateVolume(input)
	assert.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestCreateVolume_InvalidSize_NoSize(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	input := &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("ap-southeast-2a"),
	}
	_, err := svc.CreateVolume(input)
	assert.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestCreateVolume_UnsupportedVolumeType_IO1(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	input := &ec2.CreateVolumeInput{
		Size:             aws.Int64(80),
		AvailabilityZone: aws.String("ap-southeast-2a"),
		VolumeType:       aws.String("io1"),
	}
	_, err := svc.CreateVolume(input)
	assert.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestCreateVolume_UnsupportedVolumeType_GP2(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	input := &ec2.CreateVolumeInput{
		Size:             aws.Int64(80),
		AvailabilityZone: aws.String("ap-southeast-2a"),
		VolumeType:       aws.String("gp2"),
	}
	_, err := svc.CreateVolume(input)
	assert.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestCreateVolume_UnsupportedVolumeType_ST1(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	input := &ec2.CreateVolumeInput{
		Size:             aws.Int64(80),
		AvailabilityZone: aws.String("ap-southeast-2a"),
		VolumeType:       aws.String("st1"),
	}
	_, err := svc.CreateVolume(input)
	assert.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestCreateVolume_MismatchedAZ(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	input := &ec2.CreateVolumeInput{
		Size:             aws.Int64(80),
		AvailabilityZone: aws.String("us-east-1a"),
	}
	_, err := svc.CreateVolume(input)
	assert.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidAvailabilityZone, err.Error())
}

func TestCreateVolume_EmptyAZ(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	input := &ec2.CreateVolumeInput{
		Size:             aws.Int64(80),
		AvailabilityZone: aws.String(""),
	}
	_, err := svc.CreateVolume(input)
	assert.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestCreateVolume_NilAZ(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	input := &ec2.CreateVolumeInput{
		Size: aws.Int64(80),
	}
	_, err := svc.CreateVolume(input)
	assert.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

func TestCreateVolume_ValidSizeBoundary_Min(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	input := &ec2.CreateVolumeInput{
		Size:             aws.Int64(1),
		AvailabilityZone: aws.String("ap-southeast-2a"),
	}
	// This will fail at viperblock init (no S3 backend), but should pass validation
	_, err := svc.CreateVolume(input)
	// We expect an error from viperblock/S3, not from validation
	if err != nil {
		assert.NotEqual(t, awserrors.ErrorInvalidParameterValue, err.Error())
		assert.NotEqual(t, awserrors.ErrorInvalidAvailabilityZone, err.Error())
	}
}

func TestCreateVolume_ValidSizeBoundary_Max(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	input := &ec2.CreateVolumeInput{
		Size:             aws.Int64(16384),
		AvailabilityZone: aws.String("ap-southeast-2a"),
	}
	// This will fail at viperblock init (no S3 backend), but should pass validation
	_, err := svc.CreateVolume(input)
	if err != nil {
		assert.NotEqual(t, awserrors.ErrorInvalidParameterValue, err.Error())
		assert.NotEqual(t, awserrors.ErrorInvalidAvailabilityZone, err.Error())
	}
}

func TestCreateVolume_DefaultsToGP3(t *testing.T) {
	svc := newTestVolumeService("ap-southeast-2a")
	input := &ec2.CreateVolumeInput{
		Size:             aws.Int64(80),
		AvailabilityZone: aws.String("ap-southeast-2a"),
		// VolumeType not set - should default to gp3
	}
	// Passes validation, fails at viperblock (no S3 backend)
	_, err := svc.CreateVolume(input)
	if err != nil {
		assert.NotEqual(t, awserrors.ErrorInvalidParameterValue, err.Error())
		assert.NotEqual(t, awserrors.ErrorInvalidAvailabilityZone, err.Error())
	}
}
