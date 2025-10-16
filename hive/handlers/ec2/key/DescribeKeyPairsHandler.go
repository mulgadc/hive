package handlers_ec2_key

import (
	"bytes"
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

// DescribeKeyPairsHandler handles DescribeKeyPairs operations
type DescribeKeyPairsHandler struct {
	service KeyService
}

// NewDescribeKeyPairsHandler creates a new DescribeKeyPairsHandler with the given service
func NewDescribeKeyPairsHandler(service KeyService) *DescribeKeyPairsHandler {
	return &DescribeKeyPairsHandler{service: service}
}

// Topic returns the NATS topic for this handler
func (h *DescribeKeyPairsHandler) Topic() string {
	return "ec2.DescribeKeyPairs"
}

// Process handles the business logic for DescribeKeyPairs
// Note: Input validation is performed by the gateway before calling this handler
func (h *DescribeKeyPairsHandler) Process(jsonData []byte) []byte {
	var input ec2.DescribeKeyPairsInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Call the service to perform the actual operation
	result, err := h.service.DescribeKeyPairs(&input)
	if err != nil {
		slog.Error("DescribeKeyPairs service failed", "err", err)
		return utils.GenerateErrorPayload(awserrors.ErrorInternalError)
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("DescribeKeyPairsHandler could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
