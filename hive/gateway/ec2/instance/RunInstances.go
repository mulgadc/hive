package gateway_ec2_instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

type RunInstancesResponse struct {
	Reservation *ec2.Reservation `locationName:"RunInstancesResponse"`
}

/*
Sample JSON:

    "RunInstances":{
      "name":"RunInstances", // Function name
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"RunInstancesRequest"}, // Input shape from AWS API
      "output":{"shape":"Reservation"} // Output shape from AWS API
    },

    "RunInstancesRequest":{
      "type":"structure",
      "required":[
        "MaxCount",
        "MinCount"
      ],
      "members":{
        "BlockDeviceMappings":{
          "shape":"BlockDeviceMappingRequestList",
          "locationName":"BlockDeviceMapping"
        },
        "ImageId":{"shape":"ImageId"},
        "InstanceType":{"shape":"InstanceType"},
        "Ipv6AddressCount":{"shape":"Integer"},
        "Ipv6Addresses":{
          "shape":"InstanceIpv6AddressList",
          "locationName":"Ipv6Address"
        },
        "KernelId":{"shape":"KernelId"},
        "KeyName":{"shape":"KeyPairName"},
        "MaxCount":{"shape":"Integer"},
        "MinCount":{"shape":"Integer"},
        "Monitoring":{"shape":"RunInstancesMonitoringEnabled"},
        "Placement":{"shape":"Placement"},
        "RamdiskId":{"shape":"RamdiskId"},
        "SecurityGroupIds":{
          "shape":"SecurityGroupIdStringList",
          "locationName":"SecurityGroupId"
        },
        "SecurityGroups":{
          "shape":"SecurityGroupStringList",
          "locationName":"SecurityGroup"
        },
        "SubnetId":{"shape":"SubnetId"},
        "UserData":{"shape":"RunInstancesUserData"},
        "AdditionalInfo":{
          "shape":"String",
          "locationName":"additionalInfo"
        },
        "ClientToken":{
          "shape":"String",
          "idempotencyToken":true,
          "locationName":"clientToken"
        },
        "DisableApiTermination":{
          "shape":"Boolean",
          "locationName":"disableApiTermination"
        },
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        },
        "EbsOptimized":{
          "shape":"Boolean",
          "locationName":"ebsOptimized"
        },
        "IamInstanceProfile":{
          "shape":"IamInstanceProfileSpecification",
          "locationName":"iamInstanceProfile"
        },
        "InstanceInitiatedShutdownBehavior":{
          "shape":"ShutdownBehavior",
          "locationName":"instanceInitiatedShutdownBehavior"
        },
        "NetworkInterfaces":{
          "shape":"InstanceNetworkInterfaceSpecificationList",
          "locationName":"networkInterface"
        },
        "PrivateIpAddress":{
          "shape":"String",
          "locationName":"privateIpAddress"
        },
        "ElasticGpuSpecification":{"shape":"ElasticGpuSpecifications"},
        "ElasticInferenceAccelerators":{
          "shape":"ElasticInferenceAccelerators",
          "locationName":"ElasticInferenceAccelerator"
        },
        "TagSpecifications":{
          "shape":"TagSpecificationList",
          "locationName":"TagSpecification"
        },
        "LaunchTemplate":{"shape":"LaunchTemplateSpecification"},
        "InstanceMarketOptions":{"shape":"InstanceMarketOptionsRequest"},
        "CreditSpecification":{"shape":"CreditSpecificationRequest"},
        "CpuOptions":{"shape":"CpuOptionsRequest"},
        "CapacityReservationSpecification":{"shape":"CapacityReservationSpecification"},
        "HibernationOptions":{"shape":"HibernationOptionsRequest"},
        "LicenseSpecifications":{
          "shape":"LicenseSpecificationListRequest",
          "locationName":"LicenseSpecification"
        },
        "MetadataOptions":{"shape":"InstanceMetadataOptionsRequest"},
        "EnclaveOptions":{"shape":"EnclaveOptionsRequest"},
        "PrivateDnsNameOptions":{"shape":"PrivateDnsNameOptionsRequest"},
        "MaintenanceOptions":{"shape":"InstanceMaintenanceOptionsRequest"},
        "DisableApiStop":{"shape":"Boolean"},
        "EnablePrimaryIpv6":{"shape":"Boolean"}
      }
    },
*/

func RunInstances(input *ec2.RunInstancesInput) (reservation ec2.Reservation, err error) {

	// Check required args from JSON above
	// required:[
	//   "MaxCount",
	//   "MinCount"
	// ]

	if *input.MinCount == 0 {
		return reservation, errors.New("InvalidParameterValue")
	}

	if *input.MaxCount == 0 {
		return reservation, errors.New("InvalidParameterValue")
	}

	// Additional validation from EC2 spec
	if *input.MinCount > *input.MaxCount {
		return reservation, errors.New("InvalidParameterValue")
	}

	if *input.ImageId == "" {
		return reservation, errors.New("MissingParameter")
	}

	if *input.InstanceType == "" {
		return reservation, errors.New("MissingParameter")
	}

	if !strings.HasPrefix(*aws.String(*input.ImageId), "ami-") {
		return reservation, errors.New("InvalidAMIID.Malformed")

	}

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return reservation, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp, err := EC2_Process_RunInstances(jsonData)

	if err != nil {
		return reservation, fmt.Errorf("failed to process RunInstances request: %w", err)
	}

	// Unmarshal the JSON response back into a Reservation struct
	err = json.Unmarshal(jsonResp, &reservation)
	if err != nil {
		return reservation, fmt.Errorf("failed to unmarshal JSON response to Reservation: %w", err)
	}

	return reservation, nil

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
