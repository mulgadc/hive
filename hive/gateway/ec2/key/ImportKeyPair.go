package gateway_ec2_key

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	handlers_ec2_key "github.com/mulgadc/hive/hive/handlers/ec2/key"
	"github.com/nats-io/nats.go"
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

func ImportKeyPair(input *ec2.ImportKeyPairInput, natsConn *nats.Conn) (output ec2.ImportKeyPairOutput, err error) {
	// Validate input
	err = ValidateImportKeyPairInput(input)

	if err != nil {
		return output, err
	}

	// Create NATS key service and call ImportKeyPair
	keyService := handlers_ec2_key.NewNATSKeyService(natsConn)
	result, err := keyService.ImportKeyPair(input)

	if err != nil {
		return output, err
	}

	// Return the result
	output = *result
	return output, nil
}
