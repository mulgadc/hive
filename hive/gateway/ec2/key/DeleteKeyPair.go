package gateway_ec2_key

import (
	"errors"
	"log/slog"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_ec2_key "github.com/mulgadc/hive/hive/handlers/ec2/key"
	"github.com/nats-io/nats.go"
)

func ValidateDeleteKeyPairInput(input *ec2.DeleteKeyPairInput) (err error) {
	if input == nil {
		return errors.New(awserrors.ErrorMissingParameter)
	}

	// At least one of KeyName or KeyPairId must be provided
	if (input.KeyName == nil || *input.KeyName == "") && (input.KeyPairId == nil || *input.KeyPairId == "") {
		return errors.New(awserrors.ErrorMissingParameter)
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
