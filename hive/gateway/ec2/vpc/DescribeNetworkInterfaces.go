package gateway_ec2_vpc

import (
	"github.com/aws/aws-sdk-go/service/ec2"
	handlers_ec2_vpc "github.com/mulgadc/hive/hive/handlers/ec2/vpc"
	"github.com/nats-io/nats.go"
)

// DescribeNetworkInterfaces handles the EC2 DescribeNetworkInterfaces API call
func DescribeNetworkInterfaces(input *ec2.DescribeNetworkInterfacesInput, natsConn *nats.Conn) (ec2.DescribeNetworkInterfacesOutput, error) {
	svc := handlers_ec2_vpc.NewNATSVPCService(natsConn)
	result, err := svc.DescribeNetworkInterfaces(input)
	if err != nil {
		return ec2.DescribeNetworkInterfacesOutput{}, err
	}

	return *result, nil
}
