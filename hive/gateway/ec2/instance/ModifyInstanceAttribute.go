package gateway_ec2_instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// ValidateModifyInstanceAttributeInput validates the input constraints for ModifyInstanceAttribute.
// AWS rejects calls with multiple attributes set in a single request.
func ValidateModifyInstanceAttributeInput(input *ec2.ModifyInstanceAttributeInput) error {
	if input == nil {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if input.InstanceId == nil || *input.InstanceId == "" {
		return errors.New(awserrors.ErrorInvalidInstanceIDMalformed)
	}
	if !strings.HasPrefix(*input.InstanceId, "i-") {
		return errors.New(awserrors.ErrorInvalidInstanceIDMalformed)
	}

	// Exactly one attribute must be set
	count := 0
	if input.InstanceType != nil {
		count++
	}
	if input.UserData != nil {
		count++
	}
	if input.EbsOptimized != nil {
		count++
	}
	if count != 1 {
		return errors.New(awserrors.ErrorInvalidParameterValue)
	}

	// Validate instance type value is non-empty if present
	if input.InstanceType != nil && (input.InstanceType.Value == nil || *input.InstanceType.Value == "") {
		return errors.New(awserrors.ErrorInvalidInstanceAttributeValue)
	}

	return nil
}

// ModifyInstanceAttribute sends a modify request to the daemon via NATS.
// The daemon updates the stopped instance in KV and returns an empty response on success.
func ModifyInstanceAttribute(input *ec2.ModifyInstanceAttributeInput, natsConn *nats.Conn) (ec2.ModifyInstanceAttributeOutput, error) {
	if err := ValidateModifyInstanceAttributeInput(input); err != nil {
		return ec2.ModifyInstanceAttributeOutput{}, err
	}

	slog.Info("ModifyInstanceAttribute: Processing request", "instance_id", *input.InstanceId)

	jsonData, err := json.Marshal(input)
	if err != nil {
		slog.Error("ModifyInstanceAttribute: Failed to marshal request", "instance_id", *input.InstanceId, "err", err)
		return ec2.ModifyInstanceAttributeOutput{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	msg, err := natsConn.Request("ec2.ModifyInstanceAttribute", jsonData, 30*time.Second)
	if err != nil {
		slog.Error("ModifyInstanceAttribute: Failed to send request", "instance_id", *input.InstanceId, "err", err)
		return ec2.ModifyInstanceAttributeOutput{}, fmt.Errorf("failed to send modify request: %w", err)
	}

	if responseError, parseErr := utils.ValidateErrorPayload(msg.Data); parseErr != nil {
		slog.Error("ModifyInstanceAttribute: Daemon returned error", "instance_id", *input.InstanceId, "code", *responseError.Code)
		return ec2.ModifyInstanceAttributeOutput{}, errors.New(*responseError.Code)
	}

	slog.Info("ModifyInstanceAttribute: Completed successfully", "instance_id", *input.InstanceId)
	return ec2.ModifyInstanceAttributeOutput{}, nil
}
