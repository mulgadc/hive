package handlers_ec2_image

import (
	"bytes"
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

// DeregisterImageHandler handles DeregisterImage operations
type DeregisterImageHandler struct {
	service ImageService
}

// NewDeregisterImageHandler creates a new DeregisterImage handler with the given service
func NewDeregisterImageHandler(service ImageService) *DeregisterImageHandler {
	return &DeregisterImageHandler{service: service}
}

// Topic returns the NATS topic for this handler (matches AWS Action name)
func (h *DeregisterImageHandler) Topic() string {
	return "ec2.DeregisterImage"
}

// Process handles the business logic for DeregisterImage
// Note: Input validation is performed by the gateway before calling this handler
func (h *DeregisterImageHandler) Process(jsonData []byte) []byte {
	var input ec2.DeregisterImageInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Delegate to the service implementation
	result, err := h.service.DeregisterImage(&input)
	if err != nil {
		slog.Error("DeregisterImage service failed", "err", err)
		return utils.GenerateErrorPayload(awserrors.ErrorInternalError)
	}

	// Return as JSON
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("DeregisterImageHandler could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
