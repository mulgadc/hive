package handlers_ec2_image

import (
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

// ModifyImageAttributeHandler handles ModifyImageAttribute operations
type ModifyImageAttributeHandler struct {
	service ImageService
}

// NewModifyImageAttributeHandler creates a new ModifyImageAttribute handler with the given service
func NewModifyImageAttributeHandler(service ImageService) *ModifyImageAttributeHandler {
	return &ModifyImageAttributeHandler{service: service}
}

// Topic returns the NATS topic for this handler (matches AWS Action name)
func (h *ModifyImageAttributeHandler) Topic() string {
	return "ec2.ModifyImageAttribute"
}

// Process handles the business logic for ModifyImageAttribute
// Note: Input validation is performed by the gateway before calling this handler
func (h *ModifyImageAttributeHandler) Process(jsonData []byte) []byte {
	var input ec2.ModifyImageAttributeInput

	if errPayload := utils.UnmarshalJsonPayload(&input, jsonData); errPayload != nil {
		return errPayload
	}

	// Delegate to the service implementation
	result, err := h.service.ModifyImageAttribute(&input)
	if err != nil {
		slog.Error("ModifyImageAttribute service failed", "err", err)
		return utils.GenerateErrorPayload(awserrors.ErrorInternalError)
	}

	// Return as JSON
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("ModifyImageAttributeHandler could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
