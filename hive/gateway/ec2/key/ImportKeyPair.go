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

    "ImportKeyPair":{
      "name":"ImportKeyPair",
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"ImportKeyPairRequest"},
      "output":{"shape":"ImportKeyPairResult"}
    },

    "ImportKeyPairRequest":{
      "type":"structure",
      "required":[
        "KeyName",
        "PublicKeyMaterial"
      ],
      "members":{
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        },
        "KeyName":{
          "shape":"String",
          "locationName":"keyName"
        },
        "PublicKeyMaterial":{
          "shape":"Blob",
          "locationName":"publicKeyMaterial"
        },
        "TagSpecifications":{
          "shape":"TagSpecificationList",
          "locationName":"TagSpecification"
        }
      }
    }
*/

func ValidateImportKeyPairInput(input *ec2.ImportKeyPairInput) (err error) {

	// Check required args from JSON above
	// required:[
	//   "KeyName",
	//   "PublicKeyMaterial"
	// ]

	if input == nil {
		return errors.New("MissingParameter")
	}

	if input.KeyName == nil || *input.KeyName == "" {
		return errors.New("MissingParameter")
	}

	if input.PublicKeyMaterial == nil || len(input.PublicKeyMaterial) == 0 {
		return errors.New("MissingParameter")
	}

	return
}

func ImportKeyPair(input *ec2.ImportKeyPairInput) (output ec2.ImportKeyPairOutput, err error) {

	// Validate input
	err = ValidateImportKeyPairInput(input)

	if err != nil {
		return output, err
	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return output, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via handler, which will return a JSON response
	handler := handlers_ec2_key.NewImportKeyPairHandler(handlers_ec2_key.NewMockKeyService())
	jsonResp := handler.Process(jsonData)

	// Validate if the response is successful or an error
	responseError, err := utils.ValidateErrorPayload(jsonResp)

	if err != nil {
		if responseError.Code != nil {
			slog.Error("ImportKeyPair failed", "err", responseError.Code)
			return output, errors.New(*responseError.Code)
		}
		return output, err
	}

	// Unmarshal the JSON response back into output struct
	err = json.Unmarshal(jsonResp, &output)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON response to ImportKeyPairOutput: %w", err)
	}

	return output, nil
}
