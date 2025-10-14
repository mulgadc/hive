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

    "CopyImage":{
      "name":"CopyImage",
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"CopyImageRequest"},
      "output":{"shape":"CopyImageResult"}
    },

    "CopyImageRequest":{
      "type":"structure",
      "required":[
        "Name",
        "SourceImageId",
        "SourceRegion"
      ],
      "members":{
        "ClientToken":{"shape":"String"},
        "Description":{"shape":"String"},
        "Encrypted":{
          "shape":"Boolean",
          "locationName":"encrypted"
        },
        "KmsKeyId":{
          "shape":"KmsKeyId",
          "locationName":"kmsKeyId"
        },
        "Name":{"shape":"String"},
        "SourceImageId":{"shape":"String"},
        "SourceRegion":{"shape":"String"},
        "DestinationOutpostArn":{"shape":"String"},
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        },
        "CopyImageTags":{"shape":"Boolean"},
        "TagSpecifications":{
          "shape":"TagSpecificationList",
          "locationName":"TagSpecification"
        }
      }
    },

    "CopyImageResult":{
      "type":"structure",
      "members":{
        "ImageId":{
          "shape":"ImageId",
          "locationName":"imageId"
        }
      }
    }
*/

func ValidateCopyImageInput(input *ec2.CopyImageInput) (err error) {

	// Check required args from JSON above
	// required:[
	//   "Name",
	//   "SourceImageId",
	//   "SourceRegion"
	// ]

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

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp := EC2_Process_CopyImage(jsonData)

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

func EC2_Process_CopyImage(jsonData []byte) (output []byte) {

	var input ec2.CopyImageInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Ensure the payload provided the fields that EC2 expects before proceeding.
	err = ValidateCopyImageInput(&input)

	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// TODO: Add the logic to actually copy the image.
	// This is a placeholder response for testing the framework.

	result := &ec2.CopyImageOutput{
		ImageId: aws.String("ami-0987654321fedcba0"),
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("EC2_Process_CopyImage could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
