package handlers_ec2_instance

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

// RunInstancesHandler handles RunInstances operations
type RunInstancesHandler struct {
	service InstanceService
}

// NewRunInstancesHandler creates a new RunInstancesHandler with the given service
func NewRunInstancesHandler(service InstanceService) *RunInstancesHandler {
	return &RunInstancesHandler{service: service}
}

// Topic returns the NATS topic for this handler
func (h *RunInstancesHandler) Topic() string {
	return "ec2.RunInstances"
}

// Process handles the business logic for RunInstances
// Note: Input validation is performed by the gateway before calling this handler
func (h *RunInstancesHandler) Process(jsonData []byte) []byte {
	var input ec2.RunInstancesInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Validate required fields
	if input.MinCount == nil {
		return utils.GenerateErrorPayload(awserrors.ErrorMissingParameter)
	}
	if input.MaxCount == nil {
		return utils.GenerateErrorPayload(awserrors.ErrorMissingParameter)
	}
	if *input.MinCount == 0 {
		return utils.GenerateErrorPayload(awserrors.ErrorInvalidParameterValue)
	}
	if *input.MaxCount == 0 {
		return utils.GenerateErrorPayload(awserrors.ErrorInvalidParameterValue)
	}
	if *input.MinCount > *input.MaxCount {
		return utils.GenerateErrorPayload(awserrors.ErrorInvalidParameterValue)
	}
	if input.ImageId == nil || *input.ImageId == "" {
		return utils.GenerateErrorPayload(awserrors.ErrorMissingParameter)
	}
	if input.InstanceType == nil || *input.InstanceType == "" {
		return utils.GenerateErrorPayload(awserrors.ErrorMissingParameter)
	}
	if !strings.HasPrefix(*input.ImageId, "ami-") {
		return utils.GenerateErrorPayload(awserrors.ErrorInvalidAMIIDMalformed)
	}

	// Call the service to perform the actual operation
	reservation, err := h.service.RunInstances(&input)
	if err != nil {
		slog.Error("RunInstances service failed", "err", err)
		return utils.GenerateErrorPayload(awserrors.ErrorInternalError)
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(reservation)
	if err != nil {
		slog.Error("RunInstancesHandler could not marshal reservation", "err", err)
		return nil
	}

	return jsonResponse
}
