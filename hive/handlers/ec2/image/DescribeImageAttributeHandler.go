package handlers_ec2_image

import (
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

// DescribeImageAttributeHandler handles DescribeImageAttribute operations
type DescribeImageAttributeHandler struct {
	service ImageService
}

// NewDescribeImageAttributeHandler creates a new DescribeImageAttribute handler with the given service
func NewDescribeImageAttributeHandler(service ImageService) *DescribeImageAttributeHandler {
	return &DescribeImageAttributeHandler{service: service}
}

// Topic returns the NATS topic for this handler (matches AWS Action name)
func (h *DescribeImageAttributeHandler) Topic() string {
	return "ec2.DescribeImageAttribute"
}

// Process handles the business logic for DescribeImageAttribute
// Note: Input validation is performed by the gateway before calling this handler
func (h *DescribeImageAttributeHandler) Process(jsonData []byte) []byte {
	var input ec2.DescribeImageAttributeInput

	if errPayload := utils.UnmarshalJsonPayload(&input, jsonData); errPayload != nil {
		return errPayload
	}

	// Delegate to the service implementation
	result, err := h.service.DescribeImageAttribute(&input)
	if err != nil {
		slog.Error("DescribeImageAttribute service failed", "err", err)
		return utils.GenerateErrorPayload(awserrors.ErrorInternalError)
	}

	// Return as JSON
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("DescribeImageAttributeHandler could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
