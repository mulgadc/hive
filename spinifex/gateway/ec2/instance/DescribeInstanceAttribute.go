package gateway_ec2_instance

import (
	"encoding/json"
	"errors"
	"fmt"
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

	jsonData, err := json.Marshal(input)
	if err != nil {
		slog.Error("DescribeInstanceAttribute: Failed to marshal request", "instance_id", *input.InstanceId, "err", err)
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	reqMsg := nats.NewMsg("ec2.DescribeInstanceAttribute")
	reqMsg.Data = jsonData
	reqMsg.Header.Set(utils.AccountIDHeader, accountID)
	msg, err := natsConn.RequestMsg(reqMsg, 30*time.Second)
	if err != nil {
		slog.Error("DescribeInstanceAttribute: Failed to send request", "instance_id", *input.InstanceId, "err", err)
		return nil, fmt.Errorf("failed to send describe request: %w", err)
	}

	if responseError, parseErr := utils.ValidateErrorPayload(msg.Data); parseErr != nil {
		slog.Error("DescribeInstanceAttribute: Daemon returned error", "instance_id", *input.InstanceId, "code", *responseError.Code)
		return nil, errors.New(*responseError.Code)
	}

	var output ec2.DescribeInstanceAttributeOutput
	if err := json.Unmarshal(msg.Data, &output); err != nil {
		slog.Error("DescribeInstanceAttribute: Failed to unmarshal response", "instance_id", *input.InstanceId, "err", err)
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	slog.Info("DescribeInstanceAttribute: Completed successfully", "instance_id", *input.InstanceId)
	return &output, nil
}
