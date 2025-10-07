package gateway_ec2_instance

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/gofiber/fiber/v2"
	"github.com/mulgadc/hive/hive/awsec2query"
	"github.com/mulgadc/hive/hive/utils"
)

type runInstancesResponse struct {
	Reservation *ec2.Reservation `locationName:"RunInstancesResponse"`
}

type runInstancesResult struct {
	Reservation *ec2.Reservation `xml:"reservationSet>item"`
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

// AUTO-GENERATED: RunInstances
// Generated from Function name: RunInstances
func RunInstances(ctx *fiber.Ctx, args map[string]string) ([]byte, error) {

	// Check required args from JSON above
	// required:[
	//   "MaxCount",
	//   "MinCount"
	// ]
	if _, ok := args["MaxCount"]; !ok {
		return nil, errors.New("InvalidParameter")
	}
	if _, ok := args["MinCount"]; !ok {
		return nil, errors.New("InvalidParameter")
	}

	// Generated from input shape: RunInstancesRequest
	var input = &ec2.RunInstancesInput{}
	awsec2query.QueryParamsToStruct(args, input)

	// Marshal to JSON, to send over the wire for processing (NATS)
	jsonData, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input to JSON: %w", err)
	}

	// Run the simulated JSON request via NATS, which will return a JSON response
	jsonResp, err := EC2_Process_RunInstances(jsonData)

	if err != nil {
		return nil, fmt.Errorf("failed to process RunInstances request: %w", err)
	}

	// Unmarshal the JSON response back into a Reservation struct
	var reservation ec2.Reservation
	err = json.Unmarshal(jsonResp, &reservation)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON response to Reservation: %w", err)
	}

	// Convert to XML
	payload := runInstancesResponse{
		Reservation: &reservation,
	}

	output, err := utils.MarshalToXML(payload)

	if err != nil {
		return output.Bytes(), errors.New("failed to marshal response to XML")
	}

	return output.Bytes(), nil

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
