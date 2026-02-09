package handlers_ec2_key

import (
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

// DeleteKeyPairHandler handles DeleteKeyPair operations
type DeleteKeyPairHandler struct {
	service KeyService
}

// NewDeleteKeyPairHandler creates a new DeleteKeyPairHandler with the given service
func NewDeleteKeyPairHandler(service KeyService) *DeleteKeyPairHandler {
	return &DeleteKeyPairHandler{service: service}
}

// Topic returns the NATS topic for this handler
func (h *DeleteKeyPairHandler) Topic() string {
	return "ec2.DeleteKeyPair"
}

// Process handles the business logic for DeleteKeyPair
// Note: Input validation is performed by the gateway before calling this handler
func (h *DeleteKeyPairHandler) Process(jsonData []byte) []byte {
	var input ec2.DeleteKeyPairInput

	if errPayload := utils.UnmarshalJsonPayload(&input, jsonData); errPayload != nil {
		return errPayload
	}

	// Call the service to perform the actual operation
	result, err := h.service.DeleteKeyPair(&input)
	if err != nil {
		slog.Error("DeleteKeyPair service failed", "err", err)
		return utils.GenerateErrorPayload(awserrors.ErrorInternalError)
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("DeleteKeyPairHandler could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
