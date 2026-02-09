package gateway_ec2_key

import (
	"github.com/aws/aws-sdk-go/service/ec2"
	handlers_ec2_key "github.com/mulgadc/hive/hive/handlers/ec2/key"
	"github.com/nats-io/nats.go"
)

// DescribeKeyPairs lists key pairs via NATS
func DescribeKeyPairs(input *ec2.DescribeKeyPairsInput, natsConn *nats.Conn) (output ec2.DescribeKeyPairsOutput, err error) {
	// all input fields are optional filters

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
