package gateway_ec2_regions

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

// uses the loaded config file to get the region.
func DescribeRegions(input *ec2.DescribeRegionsInput, region string) (output *ec2.DescribeRegionsOutput, err error) {

	output = &ec2.DescribeRegionsOutput{
		Regions: []*ec2.Region{
			{
				RegionName: aws.String(region),
			},
		}}

	return output, nil
}
