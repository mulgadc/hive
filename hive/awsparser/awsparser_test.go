package awsparser

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/davecgh/go-spew/spew"
	"github.com/stretchr/testify/assert"
)

func TestParseRunInstance(t *testing.T) {

	runInstance := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-0abcdef1234567890"),
		InstanceType: aws.String("t2.micro"),
		MinCount:     aws.Int64(1),
		MaxCount:     aws.Int64(1),
		KeyName:      aws.String("my-key-pair"),
		SecurityGroupIds: []*string{
			aws.String("sg-0123456789abcdef0"),
		},
		SubnetId: aws.String("subnet-6e7f829e"),
	}

	// Call the function to process the RunInstances request
	response, err := EC2_RunInstances(runInstance)
	if err != nil {
		t.Fatalf("Failed to run instances: %v", err)
	}

	spew.Dump(response)
}

func TestInvalidRunInstance(t *testing.T) {

	runInstance := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-0abcdef1234567890"),
		InstanceType: aws.String("t2.micro"),
		MinCount:     aws.Int64(0),
		MaxCount:     aws.Int64(0),
		KeyName:      aws.String("my-key-pair"),
		SecurityGroupIds: []*string{
			aws.String("sg-0123456789abcdef0"),
		},
		SubnetId: aws.String("subnet-6e7f829e"),
	}
	// Call the function to process the RunInstances request
	response, err := EC2_RunInstances(runInstance)

	assert.Error(t, err, "Expected error for invalid RunInstances input")

	spew.Dump(response)
}

func TestGenerateEC2ErrorResponse(t *testing.T) {

	errorCode := "InvalidInstanceID.NotFound"
	errorMessage := "The instance ID 'i-1234567890abcdef0' does not exist"
	requestId := "123e4567-e89b-12d3-a456-426614174000"

	xmlResponse := GenerateEC2ErrorResponse(errorCode, errorMessage, requestId)

	assert.Contains(t, string(xmlResponse), "InvalidInstanceID.NotFound")
}
