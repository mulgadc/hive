package handlers_ec2_key

import (
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

// ImportKeyPairHandler handles ImportKeyPair operations
type ImportKeyPairHandler struct {
	service KeyService
}

// NewImportKeyPairHandler creates a new ImportKeyPairHandler with the given service
func NewImportKeyPairHandler(service KeyService) *ImportKeyPairHandler {
	return &ImportKeyPairHandler{service: service}
}

// Topic returns the NATS topic for this handler
func (h *ImportKeyPairHandler) Topic() string {
	return "ec2.ImportKeyPair"
}

// Process handles the business logic for ImportKeyPair
// Note: Input validation is performed by the gateway before calling this handler
func (h *ImportKeyPairHandler) Process(jsonData []byte) []byte {
	var input ec2.ImportKeyPairInput

	if errPayload := utils.UnmarshalJsonPayload(&input, jsonData); errPayload != nil {
		return errPayload
	}

	// Call the service to perform the actual operation
	result, err := h.service.ImportKeyPair(&input)
	if err != nil {
		slog.Error("ImportKeyPair service failed", "err", err)
		return utils.GenerateErrorPayload(awserrors.ErrorInternalError)
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("ImportKeyPairHandler could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
