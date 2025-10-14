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

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp := EC2_Process_DeleteKeyPair(jsonData)

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

func EC2_Process_DeleteKeyPair(jsonData []byte) (output []byte) {

	var input ec2.DeleteKeyPairInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Ensure the payload provided the fields that EC2 expects before proceeding.
	err = ValidateDeleteKeyPairInput(&input)

	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// TODO: Add the logic to actually delete the key pair.
	// This is a placeholder response for testing the framework.

	result := &ec2.DeleteKeyPairOutput{
		Return:    aws.Bool(true),
		KeyPairId: input.KeyPairId,
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("EC2_Process_DeleteKeyPair could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
