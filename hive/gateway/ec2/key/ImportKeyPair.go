package gateway_ec2_key

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

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp := EC2_Process_ImportKeyPair(jsonData)

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

func EC2_Process_ImportKeyPair(jsonData []byte) (output []byte) {

	var input ec2.ImportKeyPairInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Ensure the payload provided the fields that EC2 expects before proceeding.
	err = ValidateImportKeyPairInput(&input)

	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// TODO: Add the logic to actually import the key pair.
	// This is a placeholder response for testing the framework.

	result := &ec2.ImportKeyPairOutput{
		KeyFingerprint: aws.String("1f:51:ae:28:bf:89:e9:d8:1f:25:5d:37:2d:7d:b8:ca:9f:f5:f1:6f"),
		KeyName:        input.KeyName,
		KeyPairId:      aws.String("key-0987654321fedcba0"),
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("EC2_Process_ImportKeyPair could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
