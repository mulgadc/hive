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

func ValidateDescribeImageAttributeInput(input *ec2.DescribeImageAttributeInput) (err error) {
	if input == nil {
		return errors.New("MissingParameter")
	}

	if input.Attribute == nil || *input.Attribute == "" {
		return errors.New("MissingParameter")
	}

	if input.ImageId == nil || *input.ImageId == "" {
		return errors.New("MissingParameter")
	}

	// Validate ImageId format
	if !strings.HasPrefix(*input.ImageId, "ami-") {
		return errors.New("InvalidAMIID.Malformed")
	}

	return
}

func DescribeImageAttribute(input *ec2.DescribeImageAttributeInput) (output ec2.DescribeImageAttributeOutput, err error) {

	// Validate input
	err = ValidateDescribeImageAttributeInput(input)

	if err != nil {
		return output, err
	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return output, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via handler, which will return a JSON response
	handler := handlers_ec2_image.NewDescribeImageAttributeHandler(handlers_ec2_image.NewMockImageService())
	jsonResp := handler.Process(jsonData)

	// Validate if the response is successful or an error
	responseError, err := utils.ValidateErrorPayload(jsonResp)

	if err != nil {
		if responseError.Code != nil {
			slog.Error("DescribeImageAttribute failed", "err", responseError.Code)
			return output, errors.New(*responseError.Code)
		}
		return output, err
	}

	// Unmarshal the JSON response back into output struct
	err = json.Unmarshal(jsonResp, &output)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON response to DescribeImageAttributeOutput: %w", err)
	}

	return output, nil
}
