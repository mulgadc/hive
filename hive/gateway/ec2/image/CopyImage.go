package gateway_ec2_image

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	handlers_ec2_image "github.com/mulgadc/hive/hive/handlers/ec2/image"
	"github.com/mulgadc/hive/hive/utils"
)

func ValidateCopyImageInput(input *ec2.CopyImageInput) (err error) {
	if input == nil {
		return errors.New("MissingParameter")
	}

	if input.Name == nil || *input.Name == "" {
		return errors.New("MissingParameter")
	}

	if input.SourceImageId == nil || *input.SourceImageId == "" {
		return errors.New("MissingParameter")
	}

	if input.SourceRegion == nil || *input.SourceRegion == "" {
		return errors.New("MissingParameter")
	}

	// Validate SourceImageId format
	if !strings.HasPrefix(*input.SourceImageId, "ami-") {
		return errors.New("InvalidAMIID.Malformed")
	}

	return
}

func CopyImage(input *ec2.CopyImageInput) (output ec2.CopyImageOutput, err error) {

	// Validate input
	err = ValidateCopyImageInput(input)

	if err != nil {
		return output, err
	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return output, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via handler, which will return a JSON response
	handler := handlers_ec2_image.NewCopyImageHandler(handlers_ec2_image.NewMockImageService())
	jsonResp := handler.Process(jsonData)

	// Validate if the response is successful or an error
	responseError, err := utils.ValidateErrorPayload(jsonResp)

	if err != nil {
		if responseError.Code != nil {
			slog.Error("CopyImage failed", "err", responseError.Code)
			return output, errors.New(*responseError.Code)
		}
		return output, err
	}

	// Unmarshal the JSON response back into output struct
	err = json.Unmarshal(jsonResp, &output)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON response to CopyImageOutput: %w", err)
	}

	return output, nil
}
