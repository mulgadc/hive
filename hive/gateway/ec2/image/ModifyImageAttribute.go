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

    "ModifyImageAttribute":{
      "name":"ModifyImageAttribute",
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"ModifyImageAttributeRequest"}
    },

    "ModifyImageAttributeRequest":{
      "type":"structure",
      "required":["ImageId"],
      "members":{
        "Attribute":{"shape":"String"},
        "Description":{"shape":"AttributeValue"},
        "ImageId":{"shape":"ImageId"},
        "LaunchPermission":{"shape":"LaunchPermissionModifications"},
        "OperationType":{"shape":"OperationType"},
        "ProductCodes":{
          "shape":"ProductCodeStringList",
          "locationName":"ProductCode"
        },
        "UserGroups":{
          "shape":"UserGroupStringList",
          "locationName":"UserGroup"
        },
        "UserIds":{
          "shape":"UserIdStringList",
          "locationName":"UserId"
        },
        "Value":{"shape":"String"},
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        },
        "OrganizationArns":{
          "shape":"OrganizationArnStringList",
          "locationName":"OrganizationArn"
        },
        "OrganizationalUnitArns":{
          "shape":"OrganizationalUnitArnStringList",
          "locationName":"OrganizationalUnitArn"
        },
        "ImdsSupport":{"shape":"AttributeValue"}
      }
    }
*/

func ValidateModifyImageAttributeInput(input *ec2.ModifyImageAttributeInput) (err error) {

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

func ModifyImageAttribute(input *ec2.ModifyImageAttributeInput) (output ec2.ModifyImageAttributeOutput, err error) {

	// Validate input
	err = ValidateModifyImageAttributeInput(input)

	if err != nil {
		return output, err
	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return output, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via handler, which will return a JSON response
	handler := handlers_ec2_image.NewModifyImageAttributeHandler(handlers_ec2_image.NewMockImageService())
	jsonResp := handler.Process(jsonData)

	// Validate if the response is successful or an error
	responseError, err := utils.ValidateErrorPayload(jsonResp)

	if err != nil {
		if responseError.Code != nil {
			slog.Error("ModifyImageAttribute failed", "err", responseError.Code)
			return output, errors.New(*responseError.Code)
		}
		return output, err
	}

	// Unmarshal the JSON response back into output struct
	err = json.Unmarshal(jsonResp, &output)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON response to ModifyImageAttributeOutput: %w", err)
	}

	return output, nil
}
