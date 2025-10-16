package gateway_ec2_key

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/handlers/ec2/key"
	"github.com/mulgadc/hive/hive/utils"
)

/*
Sample JSON:

    "CreateKeyPair":{
      "name":"CreateKeyPair",
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"CreateKeyPairRequest"},
      "output":{"shape":"KeyPair"}
    },

    "CreateKeyPairRequest":{
      "type":"structure",
      "required":["KeyName"],
      "members":{
        "KeyName":{"shape":"String"},
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        },
        "KeyType":{"shape":"KeyType"},
        "TagSpecifications":{
          "shape":"TagSpecificationList",
          "locationName":"TagSpecification"
        },
        "KeyFormat":{"shape":"KeyFormat"}
      }
    }
*/

func ValidateCreateKeyPairInput(input *ec2.CreateKeyPairInput) (err error) {

	// Check required args from JSON above
	// required:["KeyName"]

	if input == nil {
		return errors.New("MissingParameter")
	}

	if input.KeyName == nil || *input.KeyName == "" {
		return errors.New("MissingParameter")
	}

	return
}

func CreateKeyPair(input *ec2.CreateKeyPairInput) (output ec2.CreateKeyPairOutput, err error) {

	// Validate input
	err = ValidateCreateKeyPairInput(input)

	if err != nil {
		return output, err
	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return output, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via handler, which will return a JSON response
	handler := handlers_ec2_key.NewCreateKeyPairHandler(handlers_ec2_key.NewMockKeyService())
	jsonResp := handler.Process(jsonData)

	// Validate if the response is successful or an error
	responseError, err := utils.ValidateErrorPayload(jsonResp)

	if err != nil {
		if responseError.Code != nil {
			slog.Error("CreateKeyPair failed", "err", responseError.Code)
			return output, errors.New(*responseError.Code)
		}
		return output, err
	}

	// Unmarshal the JSON response back into output struct
	err = json.Unmarshal(jsonResp, &output)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON response to CreateKeyPairOutput: %w", err)
	}

	return output, nil
}
