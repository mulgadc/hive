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

    "DeleteKeyPair":{
      "name":"DeleteKeyPair",
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"DeleteKeyPairRequest"},
      "output":{"shape":"DeleteKeyPairResult"}
    },

    "DeleteKeyPairRequest":{
      "type":"structure",
      "members":{
        "KeyName":{"shape":"KeyPairName"},
        "KeyPairId":{"shape":"KeyPairId"},
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        }
      }
    },

    "DeleteKeyPairResult":{
      "type":"structure",
      "members":{
        "Return":{
          "shape":"Boolean",
          "locationName":"return"
        },
        "KeyPairId":{
          "shape":"String",
          "locationName":"keyPairId"
        }
      }
    }
*/

func ValidateDeleteKeyPairInput(input *ec2.DeleteKeyPairInput) (err error) {

	// Check required args from JSON above
	// Note: Either KeyName or KeyPairId must be provided

	if input == nil {
		return errors.New("MissingParameter")
	}

	// At least one of KeyName or KeyPairId must be provided
	if (input.KeyName == nil || *input.KeyName == "") && (input.KeyPairId == nil || *input.KeyPairId == "") {
		return errors.New("MissingParameter")
	}

	return
}

func DeleteKeyPair(input *ec2.DeleteKeyPairInput) (output ec2.DeleteKeyPairOutput, err error) {

	// Validate input
	err = ValidateDeleteKeyPairInput(input)

	if err != nil {
		return output, err
	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return output, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via handler, which will return a JSON response
	handler := handlers_ec2_key.NewDeleteKeyPairHandler(handlers_ec2_key.NewMockKeyService())
	jsonResp := handler.Process(jsonData)

	// Validate if the response is successful or an error
	responseError, err := utils.ValidateErrorPayload(jsonResp)

	if err != nil {
		if responseError.Code != nil {
			slog.Error("DeleteKeyPair failed", "err", responseError.Code)
			return output, errors.New(*responseError.Code)
		}
		return output, err
	}

	// Unmarshal the JSON response back into output struct
	err = json.Unmarshal(jsonResp, &output)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON response to DeleteKeyPairOutput: %w", err)
	}

	return output, nil
}
