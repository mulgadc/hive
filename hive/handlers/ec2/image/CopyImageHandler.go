package handlers_ec2_image

import (
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

// CopyImageHandler handles CopyImage operations
type CopyImageHandler struct {
	service ImageService
}

// NewCopyImageHandler creates a new CopyImage handler with the given service
func NewCopyImageHandler(service ImageService) *CopyImageHandler {
	return &CopyImageHandler{service: service}
}

// Topic returns the NATS topic for this handler (matches AWS Action name)
func (h *CopyImageHandler) Topic() string {
	return "ec2.CopyImage"
}

// Process handles the business logic for CopyImage
// Note: Input validation is performed by the gateway before calling this handler
func (h *CopyImageHandler) Process(jsonData []byte) []byte {
	var input ec2.CopyImageInput

	if errPayload := utils.UnmarshalJsonPayload(&input, jsonData); errPayload != nil {
		return errPayload
	}

	// Delegate to the service implementation
	result, err := h.service.CopyImage(&input)
	if err != nil {
		slog.Error("CopyImage service failed", "err", err)
		return utils.GenerateErrorPayload(awserrors.ErrorInternalError)
	}

	// Return as JSON
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("CopyImageHandler could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
