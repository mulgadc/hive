package gateway_ec2_image

import (
	"fmt"

	"github.com/aws/aws-sdk-go/service/ec2"
)

func ImportImage() {

	var test = ec2.ImportImageInput{}

	fmt.Println(test)

	var t2 = ec2.ImportImageOutput{}

	fmt.Println(t2)

	var t3 = ec2.DescribeImagesInput{}

	fmt.Println(t3)

	var t4 = ec2.DescribeImagesOutput{}

	fmt.Println(t4)

}
