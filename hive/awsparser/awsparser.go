package awsparser

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/utils"
)

/*
XML error

2025-10-02 20:19:36,786 - MainThread - urllib3.connectionpool - DEBUG - https://ec2.ap-southeast-2.amazonaws.com:443 "POST / HTTP/1.1" 400 None
2025-10-02 20:19:36,787 - MainThread - botocore.parsers - DEBUG - Response headers: {'x-amzn-RequestId': 'dc74f0b6-b4dd-4aec-afb1-3d32539e0955', 'Cache-Control': 'no-cache, no-store', 'Strict-Transport-Security': 'max-age=31536000; includeSubDomains', 'vary': 'accept-encoding', 'Content-Type': 'text/xml;charset=UTF-8', 'Transfer-Encoding': 'chunked', 'Date': 'Thu, 02 Oct 2025 12:19:36 GMT', 'Connection': 'close', 'Server': 'AmazonEC2'}
2025-10-02 20:19:36,788 - MainThread - botocore.parsers - DEBUG - Response body:
b'<?xml version="1.0" encoding="UTF-8"?>\n<Response><Errors><Error><Code>InvalidParameterValue</Code><Message>Invalid value \'t3.mcro\' for InstanceType.</Message></Error></Errors><RequestID>dc74f0b6-b4dd-4aec-afb1-3d32539e0955</RequestID></Response>'

*/

type runInstancesResponse struct {
	XMLName            xml.Name           `xml:"RunInstancesResponse"`
	Xmlns              string             `xml:"xmlns,attr"`
	RunInstancesResult runInstancesResult `xml:"RunInstancesResult"`
	ResponseMetadata   responseMetadata   `xml:"ResponseMetadata"`
}

type runInstancesResult struct {
	Reservation *ec2.Reservation `xml:"reservationSet>item"`
}

type responseMetadata struct {
	RequestID string `xml:"RequestId"`
}

func EC2_Process_RunInstances(jsonData []byte) (output []byte, err error) {

	var input ec2.RunInstancesInput
	err = json.Unmarshal(jsonData, &input)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON to RunInstancesInput: %w", err)
	}

	// Here you would add the logic to actually create the instance in your system.
	// For this example, we'll just create a dummy response.

	instance := &ec2.Instance{
		InstanceId: aws.String("i-0123456789abcdef0"),
		State: &ec2.InstanceState{
			Code: aws.Int64(16),
			Name: aws.String("running"),
		},
		ImageId:      input.ImageId,
		InstanceType: input.InstanceType,
		KeyName:      input.KeyName,
		SubnetId:     input.SubnetId,
	}

	reservation := &ec2.Reservation{
		Instances: []*ec2.Instance{instance},
		OwnerId:   aws.String("123456789012"),
	}

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(reservation)
	if err != nil {
		return output, fmt.Errorf("failed to marshal reservation to JSON: %w", err)
	}

	return jsonResponse, nil

}

func EC2_RunInstances(input *ec2.RunInstancesInput) (output bytes.Buffer, err error) {

	// Simple validation
	if input.MinCount == nil || input.MaxCount == nil || *input.MinCount < 1 || *input.MaxCount < 1 || *input.MinCount > *input.MaxCount {
		return output, fmt.Errorf("Invalid MinCount or MaxCount")
	}
	if input.ImageId == nil || *input.ImageId == "" {
		return output, fmt.Errorf("ImageId is required")
	}
	if input.InstanceType == nil || *input.InstanceType == "" {
		return output, fmt.Errorf("InstanceType is required")
	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return output, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp, err := EC2_Process_RunInstances(jsonData)

	if err != nil {
		return output, fmt.Errorf("failed to process RunInstances request: %w", err)
	}

	// Unmarshal the JSON response back into a Reservation struct
	var reservation ec2.Reservation
	err = json.Unmarshal(jsonResp, &reservation)
	if err != nil {
		return output, fmt.Errorf("failed to unmarshal JSON response to Reservation: %w", err)
	}

	// Convert to XML
	payload := runInstancesResponse{
		Xmlns: "http://ec2.amazonaws.com/doc/2016-11-15/",
		RunInstancesResult: runInstancesResult{
			Reservation: &reservation,
		},
		ResponseMetadata: responseMetadata{
			RequestID: "00000000-0000-0000-0000-000000000000",
		},
	}

	output, err = utils.MarshalToXML(payload)
	if err != nil {
		return output, errors.New("failed to marshal response to XML")
	}

	return output, nil

}
