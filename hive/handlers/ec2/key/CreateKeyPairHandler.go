package handlers_ec2_key

import (
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

// CreateKeyPairHandler handles CreateKeyPair operations
type CreateKeyPairHandler struct {
	service KeyService
}

// NewCreateKeyPairHandler creates a new CreateKeyPairHandler with the given service
func NewCreateKeyPairHandler(service KeyService) *CreateKeyPairHandler {
	return &CreateKeyPairHandler{service: service}
}

// Topic returns the NATS topic for this handler
func (h *CreateKeyPairHandler) Topic() string {
	return "ec2.CreateKeyPair"
}

// Process handles the business logic for CreateKeyPair
// Note: Input validation is performed by the gateway before calling this handler
func (h *CreateKeyPairHandler) Process(jsonData []byte) []byte {
	var input ec2.CreateKeyPairInput

	if errPayload := utils.UnmarshalJsonPayload(&input, jsonData); errPayload != nil {
		return errPayload
	}

	// Call the service to perform the actual operation
	result, err := h.service.CreateKeyPair(&input)
	if err != nil {
		slog.Error("CreateKeyPair service failed", "err", err)
		return utils.GenerateErrorPayload(awserrors.ErrorInternalError)
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("CreateKeyPairHandler could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
