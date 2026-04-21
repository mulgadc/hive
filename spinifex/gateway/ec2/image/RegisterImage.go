package gateway_ec2_image

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_image "github.com/mulgadc/spinifex/spinifex/handlers/ec2/image"
	"github.com/nats-io/nats.go"
)

// validRegisterImageArchitectures is the set of architectures accepted by
// RegisterImage. Spinifex only schedules x86_64 and arm64 today, but we accept
// i386 to match the AWS SDK enum so callers passing it through unchanged don't
// fail validation.
var validRegisterImageArchitectures = map[string]bool{
	"x86_64": true,
	"arm64":  true,
	"i386":   true,
}

// ValidateRegisterImageInput validates the input parameters for RegisterImage.
//
// AMI registration in spinifex is a pointer-only operation: the caller already
// has a snapshot in Predastore and is asking us to write a config.json that
// references it. We reject every input that asks for behaviour we don't have
// (PV virtualization, S3 bundle import, kernel/ramdisk, IMDS/TPM/ENA hints)
// rather than silently accepting and discarding it.
func ValidateRegisterImageInput(input *ec2.RegisterImageInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if input.Name == nil || *input.Name == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}
	if n := len(*input.Name); n < 3 || n > 128 {
		return errors.New(awserrors.ErrorInvalidAMINameMalformed)
	}

	if input.ImageLocation != nil && *input.ImageLocation != "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if input.Architecture != nil && *input.Architecture != "" {
		if !validRegisterImageArchitectures[*input.Architecture] {
			return errors.New(awserrors.ErrorInvalidParameterValue)
		}
	}

	if input.VirtualizationType != nil && *input.VirtualizationType != "" && *input.VirtualizationType != "hvm" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	// Reject hints we don't honour rather than silently accepting them.
	if input.BootMode != nil && *input.BootMode != "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.KernelId != nil && *input.KernelId != "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.RamdiskId != nil && *input.RamdiskId != "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.TpmSupport != nil && *input.TpmSupport != "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.ImdsSupport != nil && *input.ImdsSupport != "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.EnaSupport != nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.SriovNetSupport != nil && *input.SriovNetSupport != "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if len(input.BlockDeviceMappings) == 0 {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	root := selectRootBlockDeviceMapping(input.BlockDeviceMappings, input.RootDeviceName)
	if root == nil || root.Ebs == nil || root.Ebs.SnapshotId == nil || *root.Ebs.SnapshotId == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}
	if !strings.HasPrefix(*root.Ebs.SnapshotId, "snap-") {
		return errors.New(awserrors.ErrorInvalidSnapshotIDMalformed)
	}

	return nil
}

// selectRootBlockDeviceMapping picks the BDM entry that backs the root volume.
// If RootDeviceName is set, return the entry whose DeviceName matches; otherwise
// the first entry that carries an EBS snapshot reference.
func selectRootBlockDeviceMapping(mappings []*ec2.BlockDeviceMapping, rootDeviceName *string) *ec2.BlockDeviceMapping {
	wantName := ""
	if rootDeviceName != nil {
		wantName = *rootDeviceName
	}

	if wantName != "" {
		for _, bdm := range mappings {
			if bdm == nil || bdm.DeviceName == nil {
				continue
			}
			if *bdm.DeviceName == wantName {
				return bdm
			}
		}
		return nil
	}

	for _, bdm := range mappings {
		if bdm == nil || bdm.Ebs == nil || bdm.Ebs.SnapshotId == nil {
			continue
		}
		return bdm
	}
	return nil
}

// RegisterImage handles the EC2 RegisterImage API call.
func RegisterImage(input *ec2.RegisterImageInput, natsConn *nats.Conn, accountID string) (ec2.RegisterImageOutput, error) {
	var output ec2.RegisterImageOutput

	if err := ValidateRegisterImageInput(input); err != nil {
		return output, err
	}

	svc := handlers_ec2_image.NewNATSImageService(natsConn, 0)
	result, err := svc.RegisterImage(input, accountID)
	if err != nil {
		return output, err
	}
	if result == nil {
		return output, errors.New(awserrors.ErrorServerInternal)
	}

	return *result, nil
}
