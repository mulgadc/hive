package handlers_ec2_image

import (
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

// ResetImageAttributeHandler handles ResetImageAttribute operations
type ResetImageAttributeHandler struct {
	service ImageService
}

// NewResetImageAttributeHandler creates a new ResetImageAttribute handler with the given service
func NewResetImageAttributeHandler(service ImageService) *ResetImageAttributeHandler {
	return &ResetImageAttributeHandler{service: service}
}

// Topic returns the NATS topic for this handler (matches AWS Action name)
func (h *ResetImageAttributeHandler) Topic() string {
	return "ec2.ResetImageAttribute"
}

// Process handles the business logic for ResetImageAttribute
// Note: Input validation is performed by the gateway before calling this handler
func (h *ResetImageAttributeHandler) Process(jsonData []byte) []byte {
	var input ec2.ResetImageAttributeInput

	if errPayload := utils.UnmarshalJsonPayload(&input, jsonData); errPayload != nil {
		return errPayload
	}

	// Delegate to the service implementation
	result, err := h.service.ResetImageAttribute(&input)
	if err != nil {
		slog.Error("ResetImageAttribute service failed", "err", err)
		return utils.GenerateErrorPayload(awserrors.ErrorInternalError)
	}

	// Return as JSON
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("ResetImageAttributeHandler could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
