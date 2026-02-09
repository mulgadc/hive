package handlers_ec2_image

import (
	"encoding/json"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

// DescribeImagesHandler handles DescribeImages operations
type DescribeImagesHandler struct {
	service ImageService
}

// NewDescribeImagesHandler creates a new DescribeImages handler with the given service
func NewDescribeImagesHandler(service ImageService) *DescribeImagesHandler {
	return &DescribeImagesHandler{service: service}
}

// Topic returns the NATS topic for this handler (matches AWS Action name)
func (h *DescribeImagesHandler) Topic() string {
	return "ec2.DescribeImages"
}

// Process handles the business logic for DescribeImages
// Note: Input validation is performed by the gateway before calling this handler
func (h *DescribeImagesHandler) Process(jsonData []byte) []byte {
	var input ec2.DescribeImagesInput

	if errPayload := utils.UnmarshalJsonPayload(&input, jsonData); errPayload != nil {
		return errPayload
	}

	// Delegate to the service implementation
	result, err := h.service.DescribeImages(&input)
	if err != nil {
		slog.Error("DescribeImages service failed", "err", err)
		return utils.GenerateErrorPayload(awserrors.ErrorInternalError)
	}

	// Return as JSON
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("DescribeImagesHandler could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
