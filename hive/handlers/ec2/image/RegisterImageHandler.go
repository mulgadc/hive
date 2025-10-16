package handlers_ec2_image

import (
	"bytes"
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

// RegisterImageHandler handles RegisterImage operations
type RegisterImageHandler struct {
	service ImageService
}

// NewRegisterImageHandler creates a new RegisterImage handler with the given service
func NewRegisterImageHandler(service ImageService) *RegisterImageHandler {
	return &RegisterImageHandler{service: service}
}

// Topic returns the NATS topic for this handler (matches AWS Action name)
func (h *RegisterImageHandler) Topic() string {
	return "ec2.RegisterImage"
}

// Process handles the business logic for RegisterImage
// Note: Input validation is performed by the gateway before calling this handler
func (h *RegisterImageHandler) Process(jsonData []byte) []byte {
	var input ec2.RegisterImageInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Delegate to the service implementation
	result, err := h.service.RegisterImage(&input)
	if err != nil {
		slog.Error("RegisterImage service failed", "err", err)
		return utils.GenerateErrorPayload(awserrors.ErrorInternalError)
	}

	// Return as JSON
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("RegisterImageHandler could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
