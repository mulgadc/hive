package gateway_ec2_key

import (
	"github.com/aws/aws-sdk-go/service/ec2"
	handlers_ec2_key "github.com/mulgadc/hive/hive/handlers/ec2/key"
	"github.com/nats-io/nats.go"
)

// ValidateDescribeKeyPairsInput validates the DescribeKeyPairs request
func ValidateDescribeKeyPairsInput(input *ec2.DescribeKeyPairsInput) error {
	// DescribeKeyPairs has no required parameters
	// All fields are optional filters
	return nil
}

// DescribeKeyPairs lists key pairs via NATS
func DescribeKeyPairs(input *ec2.DescribeKeyPairsInput, natsConn *nats.Conn) (output ec2.DescribeKeyPairsOutput, err error) {
	// Validate input
	err = ValidateDescribeKeyPairsInput(input)

	if err != nil {
		return output, err
	}

	// Create NATS key service and call DescribeKeyPairs
	keyService := handlers_ec2_key.NewNATSKeyService(natsConn)
	result, err := keyService.DescribeKeyPairs(input)

	if err != nil {
		return output, err
	}

	// Return the result
	output = *result
	return output, nil
}
