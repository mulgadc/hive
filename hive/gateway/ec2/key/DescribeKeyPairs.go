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

    "DescribeKeyPairs":{
      "name":"DescribeKeyPairs",
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"DescribeKeyPairsRequest"},
      "output":{"shape":"DescribeKeyPairsResult"}
    },

    "DescribeKeyPairsRequest":{
      "type":"structure",
      "members":{
        "Filters":{
          "shape":"FilterList",
          "locationName":"Filter"
        },
        "KeyNames":{
          "shape":"KeyNameStringList",
          "locationName":"KeyName"
        },
        "KeyPairIds":{
          "shape":"KeyPairIdStringList",
          "locationName":"KeyPairId"
        },
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        },
        "IncludePublicKey":{"shape":"Boolean"}
      }
    }
*/

func ValidateDescribeKeyPairsInput(input *ec2.DescribeKeyPairsInput) (err error) {

	// No required fields for DescribeKeyPairs
	// All parameters are optional

	if input == nil {
		return nil
	}

	return
}

func DescribeKeyPairs(input *ec2.DescribeKeyPairsInput) (output ec2.DescribeKeyPairsOutput, err error) {

	// Validate input
	err = ValidateDescribeKeyPairsInput(input)

	if err != nil {
		return output, err
	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return output, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp := EC2_Process_DescribeKeyPairs(jsonData)

	// Validate if the response is successful or an error
	responseError, err := utils.ValidateErrorPayload(jsonResp)

	if err != nil {
		if responseError.Code != nil {
			slog.Error("DescribeKeyPairs failed", "err", responseError.Code)
			return output, errors.New(*responseError.Code)
		}
		return output, err
	}

	// Unmarshal the JSON response back into output struct
	err = json.Unmarshal(jsonResp, &output)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON response to DescribeKeyPairsOutput: %w", err)
	}

	return output, nil
}

func EC2_Process_DescribeKeyPairs(jsonData []byte) (output []byte) {

	var input ec2.DescribeKeyPairsInput

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&input)
	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// Ensure the payload provided the fields that EC2 expects before proceeding.
	err = ValidateDescribeKeyPairsInput(&input)

	if err != nil {
		return utils.GenerateErrorPayload(awserrors.ErrorValidationError)
	}

	// TODO: Add the logic to actually query key pairs from the system.
	// This is a placeholder response for testing the framework.

	result := &ec2.DescribeKeyPairsOutput{
		KeyPairs: []*ec2.KeyPairInfo{
			{
				KeyPairId:      aws.String("key-0123456789abcdef0"),
				KeyFingerprint: aws.String("1f:51:ae:28:bf:89:e9:d8:1f:25:5d:37:2d:7d:b8:ca:9f:f5:f1:6f"),
				KeyName:        aws.String("test-key"),
				KeyType:        aws.String("rsa"),
			},
		},
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		slog.Error("EC2_Process_DescribeKeyPairs could not marshal output", "err", err)
		return nil
	}

	return jsonResponse
}
