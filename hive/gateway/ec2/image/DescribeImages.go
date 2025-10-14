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

    "DescribeImages":{
      "name":"DescribeImages",
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"DescribeImagesRequest"},
      "output":{"shape":"DescribeImagesResult"}
    },

    "DescribeImagesRequest":{
      "type":"structure",
      "members":{
        "ExecutableUsers":{
          "shape":"ExecutableByStringList",
          "locationName":"ExecutableBy"
        },
        "Filters":{
          "shape":"FilterList",
          "locationName":"Filter"
        },
        "ImageIds":{
          "shape":"ImageIdStringList",
          "locationName":"ImageId"
        },
        "Owners":{
          "shape":"OwnerStringList",
          "locationName":"Owner"
        },
        "IncludeDeprecated":{"shape":"Boolean"},
        "IncludeDisabled":{"shape":"Boolean"},
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        },
        "MaxResults":{"shape":"Integer"},
        "NextToken":{"shape":"String"}
      }
    },

    "DescribeImagesResult":{
      "type":"structure",
      "members":{
        "Images":{
          "shape":"ImageList",
          "locationName":"imagesSet"
        },
        "NextToken":{
          "shape":"String",
          "locationName":"nextToken"
        }
      }
    }
*/

func ValidateDescribeImagesInput(input *ec2.DescribeImagesInput) (err error) {

	// No required fields for DescribeImages
	// All parameters are optional

	if input == nil {
		return nil
	}

	// Validate ImageId format if provided
	if input.ImageIds != nil {
		for _, imageId := range input.ImageIds {
			if imageId != nil && !strings.HasPrefix(*imageId, "ami-") {
				return errors.New("InvalidAMIID.Malformed")
			}
		}
	}

	return
}

func DescribeImages(input *ec2.DescribeImagesInput) (output ec2.DescribeImagesOutput, err error) {

	// Validate input
	err = ValidateDescribeImagesInput(input)

	if err != nil {
		return output, err
	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return output, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp := EC2_Process_DescribeImages(jsonData)

	// Validate if the response is successful or an error
	responseError, err := utils.ValidateErrorPayload(jsonResp)

	if err != nil {
		if responseError.Code != nil {
			slog.Error("DescribeImages failed", "err", responseError.Code)
			return output, errors.New(*responseError.Code)
		}
		return output, err
	}

	// Unmarshal the JSON response back into output struct
	err = json.Unmarshal(jsonResp, &output)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON response to DescribeImagesOutput: %w", err)
	}

	return output, nil
}

func EC2_Process_DescribeImages(jsonData []byte) (output []byte) {

	var input ec2.DescribeImagesInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Ensure the payload provided the fields that EC2 expects before proceeding.
	err = ValidateDescribeImagesInput(&input)

	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// TODO: Add the logic to actually query images from the system.
	// This is a placeholder response for testing the framework.

	result := &ec2.DescribeImagesOutput{
		Images: []*ec2.Image{
			{
				ImageId:      aws.String("ami-0123456789abcdef0"),
				Name:         aws.String("test-image"),
				State:        aws.String("available"),
				Architecture: aws.String("x86_64"),
				ImageType:    aws.String("machine"),
				OwnerId:      aws.String("123456789012"),
				Public:       aws.Bool(false),
			},
		},
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("EC2_Process_DescribeImages could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
