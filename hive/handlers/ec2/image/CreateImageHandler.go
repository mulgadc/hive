package handlers_ec2_image

import (
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

// CreateImageHandler handles CreateImage operations
type CreateImageHandler struct {
	service ImageService
}

// NewCreateImageHandler creates a new CreateImage handler with the given service
func NewCreateImageHandler(service ImageService) *CreateImageHandler {
	return &CreateImageHandler{service: service}
}

// Topic returns the NATS topic for this handler (matches AWS Action name)
func (h *CreateImageHandler) Topic() string {
	return "ec2.CreateImage"
}

// Process handles the business logic for CreateImage
// Note: Input validation is performed by the gateway before calling this handler
func (h *CreateImageHandler) Process(jsonData []byte) []byte {
	var input ec2.CreateImageInput

	if errPayload := utils.UnmarshalJsonPayload(&input, jsonData); errPayload != nil {
		return errPayload
	}

	// Delegate to the service implementation
	result, err := h.service.CreateImage(&input)
	if err != nil {
		slog.Error("CreateImage service failed", "err", err)
		return utils.GenerateErrorPayload(awserrors.ErrorInternalError)
	}

	// Return as JSON
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("CreateImageHandler could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
