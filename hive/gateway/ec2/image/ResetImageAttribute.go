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

    "ResetImageAttribute":{
      "name":"ResetImageAttribute",
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"ResetImageAttributeRequest"}
    },

    "ResetImageAttributeRequest":{
      "type":"structure",
      "required":[
        "Attribute",
        "ImageId"
      ],
      "members":{
        "Attribute":{"shape":"ResetImageAttributeName"},
        "ImageId":{"shape":"ImageId"},
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        }
      }
    }
*/

func ValidateResetImageAttributeInput(input *ec2.ResetImageAttributeInput) (err error) {

	// Check required args from JSON above
	// required:[
	//   "Attribute",
	//   "ImageId"
	// ]

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

func ResetImageAttribute(input *ec2.ResetImageAttributeInput) (output ec2.ResetImageAttributeOutput, err error) {

	// Validate input
	err = ValidateResetImageAttributeInput(input)

	if err != nil {
		return output, err
	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return output, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp := EC2_Process_ResetImageAttribute(jsonData)

	// Validate if the response is successful or an error
	responseError, err := utils.ValidateErrorPayload(jsonResp)

	if err != nil {
		if responseError.Code != nil {
			slog.Error("ResetImageAttribute failed", "err", responseError.Code)
			return output, errors.New(*responseError.Code)
		}
		return output, err
	}

	// Unmarshal the JSON response back into output struct
	err = json.Unmarshal(jsonResp, &output)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON response to ResetImageAttributeOutput: %w", err)
	}

	return output, nil
}

func EC2_Process_ResetImageAttribute(jsonData []byte) (output []byte) {

	var input ec2.ResetImageAttributeInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Ensure the payload provided the fields that EC2 expects before proceeding.
	err = ValidateResetImageAttributeInput(&input)

	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// TODO: Add the logic to actually reset image attributes.
	// This is a placeholder response for testing the framework.

	// ResetImageAttributeOutput has no exported fields, so we return a simple success indicator
	// to distinguish from error responses
	result := map[string]bool{"Return": true}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("EC2_Process_ResetImageAttribute could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
