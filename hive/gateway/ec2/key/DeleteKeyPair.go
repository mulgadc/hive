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

func DeleteKeyPair(input *ec2.DeleteKeyPairInput, natsConn *nats.Conn) (output ec2.DeleteKeyPairOutput, err error) {

	// Validate input
	err = ValidateDeleteKeyPairInput(input)

	if err != nil {
		return output, err
	}

	// Create NATS key service and call DeleteKeyPair
	keyService := handlers_ec2_key.NewNATSKeyService(natsConn)
	result, err := keyService.DeleteKeyPair(input)

	if err != nil {
		slog.Error("DeleteKeyPair failed", "err", err)
		return output, err
	}

	// Return the result
	output = *result
	return output, nil
}
