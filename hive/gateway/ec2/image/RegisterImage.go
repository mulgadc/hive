package gateway_ec2_image

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
)

/*
Sample JSON:

    "RegisterImage":{
      "name":"RegisterImage",
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"RegisterImageRequest"},
      "output":{"shape":"RegisterImageResult"}
    },

    "RegisterImageRequest":{
      "type":"structure",
      "required":["Name"],
      "members":{
        "ImageLocation":{"shape":"String"},
        "Architecture":{
          "shape":"ArchitectureValues",
          "locationName":"architecture"
        },
        "BlockDeviceMappings":{
          "shape":"BlockDeviceMappingRequestList",
          "locationName":"BlockDeviceMapping"
        },
        "Description":{
          "shape":"String",
          "locationName":"description"
        },
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        },
        "EnaSupport":{
          "shape":"Boolean",
          "locationName":"enaSupport"
        },
        "KernelId":{
          "shape":"KernelId",
          "locationName":"kernelId"
        },
        "Name":{
          "shape":"String",
          "locationName":"name"
        },
        "BillingProducts":{
          "shape":"BillingProductList",
          "locationName":"BillingProduct"
        },
        "RamdiskId":{
          "shape":"RamdiskId",
          "locationName":"ramdiskId"
        },
        "RootDeviceName":{
          "shape":"String",
          "locationName":"rootDeviceName"
        },
        "SriovNetSupport":{
          "shape":"String",
          "locationName":"sriovNetSupport"
        },
        "VirtualizationType":{
          "shape":"String",
          "locationName":"virtualizationType"
        },
        "BootMode":{
          "shape":"BootModeValues",
          "locationName":"bootMode"
        },
        "TpmSupport":{
          "shape":"TpmSupportValues"
        },
        "UefiData":{"shape":"StringType"},
        "ImdsSupport":{
          "shape":"ImdsSupportValues"
        },
        "TagSpecifications":{
          "shape":"TagSpecificationList",
          "locationName":"TagSpecification"
        }
      }
    },

    "RegisterImageResult":{
      "type":"structure",
      "members":{
        "ImageId":{
          "shape":"ImageId",
          "locationName":"imageId"
        }
      }
    }
*/

func ValidateRegisterImageInput(input *ec2.RegisterImageInput) (err error) {

	// Check required args from JSON above
	// required:["Name"]

	if input == nil {
		return errors.New("MissingParameter")
	}

	if input.Name == nil || *input.Name == "" {
		return errors.New("MissingParameter")
	}

	return
}

func RegisterImage(input *ec2.RegisterImageInput) (output ec2.RegisterImageOutput, err error) {

	// Validate input
	err = ValidateRegisterImageInput(input)

	if err != nil {
		return output, err
	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return output, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp := EC2_Process_RegisterImage(jsonData)

	// Validate if the response is successful or an error
	responseError, err := utils.ValidateErrorPayload(jsonResp)

	if err != nil {
		if responseError.Code != nil {
			slog.Error("RegisterImage failed", "err", responseError.Code)
			return output, errors.New(*responseError.Code)
		}
		return output, err
	}

	// Unmarshal the JSON response back into output struct
	err = json.Unmarshal(jsonResp, &output)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON response to RegisterImageOutput: %w", err)
	}

	return output, nil
}

func EC2_Process_RegisterImage(jsonData []byte) (output []byte) {

	var input ec2.RegisterImageInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Ensure the payload provided the fields that EC2 expects before proceeding.
	err = ValidateRegisterImageInput(&input)

	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// TODO: Add the logic to actually register the image.
	// This is a placeholder response for testing the framework.

	result := &ec2.RegisterImageOutput{
		ImageId: aws.String("ami-0123456789abcdef0"),
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("EC2_Process_RegisterImage could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
