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

    "DescribeImageAttribute":{
      "name":"DescribeImageAttribute",
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"DescribeImageAttributeRequest"},
      "output":{"shape":"ImageAttribute"}
    },

    "DescribeImageAttributeRequest":{
      "type":"structure",
      "required":[
        "Attribute",
        "ImageId"
      ],
      "members":{
        "Attribute":{"shape":"ImageAttributeName"},
        "ImageId":{"shape":"ImageId"},
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        }
      }
    },

    "ImageAttribute":{
      "type":"structure",
      "members":{
        "BlockDeviceMappings":{
          "shape":"BlockDeviceMappingList",
          "locationName":"blockDeviceMapping"
        },
        "ImageId":{
          "shape":"ImageId",
          "locationName":"imageId"
        },
        "LaunchPermissions":{
          "shape":"LaunchPermissionList",
          "locationName":"launchPermission"
        },
        "ProductCodes":{
          "shape":"ProductCodeList",
          "locationName":"productCodes"
        },
        "Description":{"shape":"AttributeValue"},
        "KernelId":{"shape":"AttributeValue"},
        "RamdiskId":{"shape":"AttributeValue"},
        "SriovNetSupport":{"shape":"AttributeValue"},
        "BootMode":{"shape":"AttributeValue"},
        "TpmSupport":{"shape":"AttributeValue"},
        "UefiData":{"shape":"AttributeValue"},
        "LastLaunchedTime":{"shape":"AttributeValue"},
        "ImdsSupport":{"shape":"AttributeValue"},
        "DeregistrationProtection":{"shape":"AttributeValue"}
      }
    }
*/

func ValidateDescribeImageAttributeInput(input *ec2.DescribeImageAttributeInput) (err error) {

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
