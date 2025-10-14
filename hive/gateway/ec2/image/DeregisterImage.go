package gateway_ec2_image

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
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

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp := EC2_Process_DeregisterImage(jsonData)

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

func EC2_Process_DeregisterImage(jsonData []byte) (output []byte) {

	var input ec2.DeregisterImageInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Ensure the payload provided the fields that EC2 expects before proceeding.
	err = ValidateDeregisterImageInput(&input)

	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// TODO: Add the logic to actually deregister the image.
	// This is a placeholder response for testing the framework.

	// DeregisterImageOutput has no exported fields, so we return a simple success indicator
	// to distinguish from error responses
	result := map[string]bool{"Return": true}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("EC2_Process_DeregisterImage could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
