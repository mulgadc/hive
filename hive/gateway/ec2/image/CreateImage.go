package gateway_ec2_image

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

/*
Sample JSON:

    "CreateImage":{
      "name":"CreateImage",
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"CreateImageRequest"},
      "output":{"shape":"CreateImageResult"}
    },

    "CreateImageRequest":{
      "type":"structure",
      "required":[
        "InstanceId",
        "Name"
      ],
      "members":{
        "BlockDeviceMappings":{
          "shape":"BlockDeviceMappingRequestList",
          "locationName":"blockDeviceMapping"
        },
        "Description":{
          "shape":"String",
          "locationName":"description"
        },
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        },
        "InstanceId":{
          "shape":"InstanceId",
          "locationName":"instanceId"
        },
        "Name":{
          "shape":"String",
          "locationName":"name"
        },
        "NoReboot":{
          "shape":"Boolean",
          "locationName":"noReboot"
        },
        "TagSpecifications":{
          "shape":"TagSpecificationList",
          "locationName":"TagSpecification"
        }
      }
    },

    "CreateImageResult":{
      "type":"structure",
      "members":{
        "ImageId":{
          "shape":"ImageId",
          "locationName":"imageId"
        }
      }
    }
*/

func ValidateCreateImageInput(input *ec2.CreateImageInput) (err error) {

	// Check required args from JSON above
	// required:[
	//   "InstanceId",
	//   "Name"
	// ]

	if input == nil {
		return errors.New("MissingParameter")
	}

	if input.InstanceId == nil || *input.InstanceId == "" {
		return errors.New("MissingParameter")
	}

	if input.Name == nil || *input.Name == "" {
		return errors.New("MissingParameter")
	}

	// Validate InstanceId format
	if !strings.HasPrefix(*input.InstanceId, "i-") {
		return errors.New("InvalidInstanceID.Malformed")
	}

	return
}

func CreateImage(input *ec2.CreateImageInput) (output ec2.CreateImageOutput, err error) {

	// Validate input
	err = ValidateCreateImageInput(input)

	if err != nil {
		return output, err
	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return output, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp := EC2_Process_CreateImage(jsonData)

	// Validate if the response is successful or an error
	responseError, err := utils.ValidateErrorPayload(jsonResp)

	if err != nil {
		if responseError.Code != nil {
			slog.Error("CreateImage failed", "err", responseError.Code)
			return output, errors.New(*responseError.Code)
		}
		return output, err
	}

	// Unmarshal the JSON response back into output struct
	err = json.Unmarshal(jsonResp, &output)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON response to CreateImageOutput: %w", err)
	}

	return output, nil
}

func EC2_Process_CreateImage(jsonData []byte) (output []byte) {

	var input ec2.CreateImageInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Ensure the payload provided the fields that EC2 expects before proceeding.
	err = ValidateCreateImageInput(&input)

	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// TODO: Add the logic to actually create the image from the instance.
	// This is a placeholder response for testing the framework.

	result := &ec2.CreateImageOutput{
		ImageId: aws.String("ami-0123456789abcdef0"),
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("EC2_Process_CreateImage could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
