package handlers_ec2_instance

import (
	"encoding/json"
	"log/slog"

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

	if errPayload := utils.UnmarshalJsonPayload(&input, jsonData); errPayload != nil {
		return errPayload
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
