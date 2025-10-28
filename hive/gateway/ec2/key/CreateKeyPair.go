package gateway_ec2_key

import (
	"errors"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/handlers/ec2/key"
	"github.com/nats-io/nats.go"
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

func CreateKeyPair(input *ec2.CreateKeyPairInput, natsConn *nats.Conn) (output ec2.CreateKeyPairOutput, err error) {

	// Validate input
	err = ValidateCreateKeyPairInput(input)

	if err != nil {
		return output, err
	}

	// Create NATS key service and call CreateKeyPair
	keyService := handlers_ec2_key.NewNATSKeyService(natsConn)
	result, err := keyService.CreateKeyPair(input)

	if err != nil {
		slog.Error("CreateKeyPair failed", "err", err)
		return output, err
	}

	// Return the result
	output = *result
	return output, nil
}
