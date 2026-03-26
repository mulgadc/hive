package gateway_ec2_instance

import (
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

// ValidateDescribeInstanceAttributeInput validates the input for DescribeInstanceAttribute.
func ValidateDescribeInstanceAttributeInput(input *ec2.DescribeInstanceAttributeInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.InstanceId == nil || *input.InstanceId == "" {
		return errors.New(awserrors.ErrorInvalidInstanceIDMalformed)
	}
	if !strings.HasPrefix(*input.InstanceId, "i-") {
		return errors.New(awserrors.ErrorInvalidInstanceIDMalformed)
	}
	if input.Attribute == nil || *input.Attribute == "" {
		return errors.New(awserrors.ErrorMissingParameter)
	}
	return nil
}

// DescribeInstanceAttribute sends a describe-attribute request to the daemon via NATS.
func DescribeInstanceAttribute(input *ec2.DescribeInstanceAttributeInput, natsConn *nats.Conn, accountID string) (*ec2.DescribeInstanceAttributeOutput, error) {
	if err := ValidateDescribeInstanceAttributeInput(input); err != nil {
		return nil, err
	}

	slog.Info("DescribeInstanceAttribute: Processing request", "instance_id", *input.InstanceId, "attribute", *input.Attribute)

	output, err := utils.NATSRequest[ec2.DescribeInstanceAttributeOutput](natsConn, "ec2.DescribeInstanceAttribute", input, 30*time.Second, accountID)
	if err != nil {
		return nil, err
	}

	slog.Info("DescribeInstanceAttribute: Completed successfully", "instance_id", *input.InstanceId)
	return output, nil
}
