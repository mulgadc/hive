package gateway_ec2_vpc

import (
	"github.com/aws/aws-sdk-go/service/ec2"
	handlers_ec2_vpc "github.com/mulgadc/hive/hive/handlers/ec2/vpc"
	"github.com/nats-io/nats.go"
)

// DescribeSubnets handles the EC2 DescribeSubnets API call
func DescribeSubnets(input *ec2.DescribeSubnetsInput, natsConn *nats.Conn) (ec2.DescribeSubnetsOutput, error) {
	var output ec2.DescribeSubnetsOutput

	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.DescribeSubnets(input)
	if err != nil {
		return output, err
	}

	return *result, nil
}
