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

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp := EC2_Process_CreateKeyPair(jsonData)

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

func EC2_Process_CreateKeyPair(jsonData []byte) (output []byte) {

	var input ec2.CreateKeyPairInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Ensure the payload provided the fields that EC2 expects before proceeding.
	err = ValidateCreateKeyPairInput(&input)

	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// TODO: Add the logic to actually create the key pair.
	// This is a placeholder response for testing the framework.

	result := &ec2.CreateKeyPairOutput{
		KeyFingerprint: aws.String("1f:51:ae:28:bf:89:e9:d8:1f:25:5d:37:2d:7d:b8:ca:9f:f5:f1:6f"),
		KeyMaterial:    aws.String("-----BEGIN RSA PRIVATE KEY-----\nMIIEpQIBAAKCAQEA...\n-----END RSA PRIVATE KEY-----"),
		KeyName:        input.KeyName,
		KeyPairId:      aws.String("key-0123456789abcdef0"),
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("EC2_Process_CreateKeyPair could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
