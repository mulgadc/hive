package gateway_ec2_image

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/handlers/ec2/image"
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

	// Run the simulated JSON request via handler, which will return a JSON response
	handler := handlers_ec2_image.NewRegisterImageHandler(handlers_ec2_image.NewMockImageService())
	jsonResp := handler.Process(jsonData)

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
