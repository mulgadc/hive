package gateway_ec2_instance

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	handlers_ec2_instance "github.com/mulgadc/hive/hive/handlers/ec2/instance"
	"github.com/nats-io/nats.go"
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

func ValidateRunInstancesInput(input *ec2.RunInstancesInput) (err error) {

	// Check required args from JSON above
	// required:[
	//   "MaxCount",
	//   "MinCount"
	// ]

	if input == nil {
		return errors.New("MissingParameter")
	}

	if input.MinCount == nil {
		return errors.New("MissingParameter")
	}

	if *input.MinCount == 0 {
		return errors.New("InvalidParameterValue")
	}

	if *input.MaxCount == 0 {
		return errors.New("InvalidParameterValue")
	}

	// Additional validation from EC2 spec
	if *input.MinCount > *input.MaxCount {
		return errors.New("InvalidParameterValue")
	}

	if input.ImageId == nil || *input.ImageId == "" {
		return errors.New("MissingParameter")
	}

	if input.InstanceType == nil || *input.InstanceType == "" {
		return errors.New("MissingParameter")
	}

	if !strings.HasPrefix(*input.ImageId, "ami-") {
		return errors.New("InvalidAMIID.Malformed")

	}

	return

}

func RunInstances(input *ec2.RunInstancesInput, natsConn *nats.Conn) (reservation ec2.Reservation, err error) {

	// Validate input
	err = ValidateRunInstancesInput(input)

	if err != nil {
		return reservation, err
	}

	// Create NATS-based instance service
	service := handlers_ec2_instance.NewNATSInstanceService(natsConn)

	// Call the service directly (no need for JSON marshaling/unmarshaling in same process)
	reservationPtr, err := service.RunInstances(input)
	if err != nil {
		return reservation, err
	}

	// Dereference pointer to return value
	return *reservationPtr, nil
}
