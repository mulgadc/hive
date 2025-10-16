package gateway_ec2_image

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/handlers/ec2/image"
	"github.com/mulgadc/hive/hive/utils"
)

/*
Sample JSON:

    "DeregisterImage":{
      "name":"DeregisterImage",
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"DeregisterImageRequest"}
    },

    "DeregisterImageRequest":{
      "type":"structure",
      "required":["ImageId"],
      "members":{
        "ImageId":{"shape":"ImageId"},
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        }
      }
    }
*/

func ValidateDeregisterImageInput(input *ec2.DeregisterImageInput) (err error) {

	// Check required args from JSON above
	// required:["ImageId"]

	if input == nil {
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

func DeregisterImage(input *ec2.DeregisterImageInput) (output ec2.DeregisterImageOutput, err error) {

	// Validate input
	err = ValidateDeregisterImageInput(input)

	if err != nil {
		return output, err
	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return output, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via handler, which will return a JSON response
	handler := handlers_ec2_image.NewDeregisterImageHandler(handlers_ec2_image.NewMockImageService())
	jsonResp := handler.Process(jsonData)

	// Validate if the response is successful or an error
	responseError, err := utils.ValidateErrorPayload(jsonResp)

	if err != nil {
		if responseError.Code != nil {
			slog.Error("DeregisterImage failed", "err", responseError.Code)
			return output, errors.New(*responseError.Code)
		}
		return output, err
	}

	// Unmarshal the JSON response back into output struct
	err = json.Unmarshal(jsonResp, &output)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON response to DeregisterImageOutput: %w", err)
	}

	return output, nil
}
