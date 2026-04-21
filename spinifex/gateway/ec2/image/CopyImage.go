package gateway_ec2_image

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	handlers_ec2_image "github.com/mulgadc/spinifex/spinifex/handlers/ec2/image"
	"github.com/nats-io/nats.go"
)

// ValidateCopyImageInput validates the input parameters for CopyImage.
//
// Spinifex is single-region and the copy is metadata-only (the new snapshot
// inherits the source's VolumeID, so no block copy runs). We reject flags that
// imply behaviour we don't support — cross-region, encryption, Outposts — at
// the gateway so the daemon never has to think about them. ClientToken is
// accepted but not honoured; retries produce distinct AMIs.
func ValidateCopyImageInput(input *ec2.CopyImageInput, gwRegion string) error {
	if input == nil {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	if input.Name == nil || *input.Name == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}
	if n := len(*input.Name); n < 3 || n > 128 {
		return errors.New(awserrors.ErrorInvalidAMINameMalformed)
	}

	if input.SourceImageId == nil || *input.SourceImageId == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}
	if !strings.HasPrefix(*input.SourceImageId, "ami-") {
		return errors.New(awserrors.ErrorInvalidAMIIDMalformed)
	}

	if input.SourceRegion == nil || *input.SourceRegion == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}
	if *input.SourceRegion != gwRegion {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	if input.Encrypted != nil && *input.Encrypted {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.KmsKeyId != nil && *input.KmsKeyId != "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.DestinationOutpostArn != nil && *input.DestinationOutpostArn != "" {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	return nil
}

// CopyImage handles the EC2 CopyImage API call.
func CopyImage(input *ec2.CopyImageInput, natsConn *nats.Conn, gwRegion, accountID string) (ec2.CopyImageOutput, error) {
	var output ec2.CopyImageOutput

	if err := ValidateCopyImageInput(input, gwRegion); err != nil {
		return output, err
	}

	svc := handlers_ec2_image.NewNATSImageService(natsConn, 0)
	result, err := svc.CopyImage(input, accountID)
	if err != nil {
		return output, err
	}
	if result == nil {
		return output, errors.New(awserrors.ErrorServerInternal)
	}

	return *result, nil
}
