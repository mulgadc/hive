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

	// Run the simulated JSON request via handler, which will return a JSON response
	handler := handlers_ec2_key.NewDescribeKeyPairsHandler(handlers_ec2_key.NewMockKeyService())
	jsonResp := handler.Process(jsonData)

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
