// Package gateway provides the AWS Gateway for the Hive platform.
// It handles the incoming requests from the AWS SDK and delegates to the appropriate gateway functions (which calls the NATS handlers).
package gateway

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/gofiber/fiber/v2"
	"github.com/mulgadc/hive/hive/awsec2query"
	"github.com/mulgadc/hive/hive/awserrors"
	gateway_ec2_account "github.com/mulgadc/hive/hive/gateway/ec2/account"
	gateway_ec2_eigw "github.com/mulgadc/hive/hive/gateway/ec2/eigw"
	gateway_ec2_igw "github.com/mulgadc/hive/hive/gateway/ec2/igw"
	gateway_ec2_image "github.com/mulgadc/hive/hive/gateway/ec2/image"
	gateway_ec2_instance "github.com/mulgadc/hive/hive/gateway/ec2/instance"
	gateway_ec2_key "github.com/mulgadc/hive/hive/gateway/ec2/key"
	gateway_ec2_snapshot "github.com/mulgadc/hive/hive/gateway/ec2/snapshot"
	gateway_ec2_tags "github.com/mulgadc/hive/hive/gateway/ec2/tags"
	gateway_ec2_volume "github.com/mulgadc/hive/hive/gateway/ec2/volume"
	gateway_ec2_vpc "github.com/mulgadc/hive/hive/gateway/ec2/vpc"
	gateway_ec2_zone "github.com/mulgadc/hive/hive/gateway/ec2/zone"
	"github.com/mulgadc/hive/hive/utils"
)

// EC2Handler processes parsed query args and returns XML response bytes.
// The action parameter is the EC2 API action name, passed from the map key.
type EC2Handler func(action string, q map[string]string, gw *GatewayConfig) ([]byte, error)

// ec2Handler creates a type-safe EC2Handler that allocates the typed input struct,
// parses query params into it, calls the handler, and marshals the output to XML.
func ec2Handler[In any](handler func(*In, *GatewayConfig) (any, error)) EC2Handler {
	return func(action string, q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := new(In)
		if err := awsec2query.QueryParamsToStruct(q, input); err != nil {
			return nil, err
		}
		output, err := handler(input, gw)
		if err != nil {
			return nil, err
		}
		payload := utils.GenerateXMLPayload(action+"Response", output)
		xmlOutput, err := utils.MarshalToXML(payload)
		if err != nil {
			return nil, errors.New("failed to marshal response to XML")
		}
		return xmlOutput, nil
	}
}

var ec2Actions = map[string]EC2Handler{
	"DescribeInstances": ec2Handler(func(input *ec2.DescribeInstancesInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_instance.DescribeInstances(input, gw.NATSConn, gw.DiscoverActiveNodes())
	}),
	"RunInstances": ec2Handler(func(input *ec2.RunInstancesInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_instance.RunInstances(input, gw.NATSConn)
	}),
	"StartInstances": ec2Handler(func(input *ec2.StartInstancesInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_instance.StartInstances(input, gw.NATSConn)
	}),
	"StopInstances": ec2Handler(func(input *ec2.StopInstancesInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_instance.StopInstances(input, gw.NATSConn)
	}),
	"TerminateInstances": ec2Handler(func(input *ec2.TerminateInstancesInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_instance.TerminateInstances(input, gw.NATSConn)
	}),
	"DescribeInstanceTypes": ec2Handler(func(input *ec2.DescribeInstanceTypesInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_instance.DescribeInstanceTypes(input, gw.NATSConn, gw.ExpectedNodes)
	}),
	"GetConsoleOutput": ec2Handler(func(input *ec2.GetConsoleOutputInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_instance.GetConsoleOutput(input, gw.NATSConn)
	}),
	"ModifyInstanceAttribute": ec2Handler(func(input *ec2.ModifyInstanceAttributeInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_instance.ModifyInstanceAttribute(input, gw.NATSConn)
	}),
	"CreateKeyPair": ec2Handler(func(input *ec2.CreateKeyPairInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_key.CreateKeyPair(input, gw.NATSConn)
	}),
	"DeleteKeyPair": ec2Handler(func(input *ec2.DeleteKeyPairInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_key.DeleteKeyPair(input, gw.NATSConn)
	}),
	"DescribeKeyPairs": ec2Handler(func(input *ec2.DescribeKeyPairsInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_key.DescribeKeyPairs(input, gw.NATSConn)
	}),
	"ImportKeyPair": func(action string, q map[string]string, gw *GatewayConfig) ([]byte, error) {
		// Workaround: parser leaves Base64 padding URL-encoded
		if strings.HasSuffix(q["PublicKeyMaterial"], "%3D%3D") {
			q["PublicKeyMaterial"] = strings.Replace(q["PublicKeyMaterial"], "%3D%3D", "==", 1)
		}
		return ec2Handler(func(input *ec2.ImportKeyPairInput, gw *GatewayConfig) (any, error) {
			return gateway_ec2_key.ImportKeyPair(input, gw.NATSConn)
		})(action, q, gw)
	},
	"DescribeImages": ec2Handler(func(input *ec2.DescribeImagesInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_image.DescribeImages(input, gw.NATSConn)
	}),
	"CreateImage": ec2Handler(func(input *ec2.CreateImageInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_image.CreateImage(input, gw.NATSConn)
	}),
	"DescribeRegions": ec2Handler(func(input *ec2.DescribeRegionsInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_zone.DescribeRegions(input, gw.Region)
	}),
	"DescribeAvailabilityZones": ec2Handler(func(input *ec2.DescribeAvailabilityZonesInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_zone.DescribeAvailabilityZones(input, gw.Region, gw.AZ)
	}),
	"DescribeVolumes": ec2Handler(func(input *ec2.DescribeVolumesInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_volume.DescribeVolumes(input, gw.NATSConn)
	}),
	"ModifyVolume": ec2Handler(func(input *ec2.ModifyVolumeInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_volume.ModifyVolume(input, gw.NATSConn)
	}),
	"CreateVolume": ec2Handler(func(input *ec2.CreateVolumeInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_volume.CreateVolume(input, gw.NATSConn)
	}),
	"DeleteVolume": ec2Handler(func(input *ec2.DeleteVolumeInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_volume.DeleteVolume(input, gw.NATSConn)
	}),
	"AttachVolume": ec2Handler(func(input *ec2.AttachVolumeInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_volume.AttachVolume(input, gw.NATSConn)
	}),
	"DescribeVolumeStatus": ec2Handler(func(input *ec2.DescribeVolumeStatusInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_volume.DescribeVolumeStatus(input, gw.NATSConn)
	}),
	"DetachVolume": ec2Handler(func(input *ec2.DetachVolumeInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_volume.DetachVolume(input, gw.NATSConn)
	}),
	"DescribeAccountAttributes": ec2Handler(func(input *ec2.DescribeAccountAttributesInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_account.DescribeAccountAttributes(input)
	}),
	"EnableEbsEncryptionByDefault": ec2Handler(func(input *ec2.EnableEbsEncryptionByDefaultInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_account.EnableEbsEncryptionByDefault(input, gw.NATSConn)
	}),
	"DisableEbsEncryptionByDefault": ec2Handler(func(input *ec2.DisableEbsEncryptionByDefaultInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_account.DisableEbsEncryptionByDefault(input, gw.NATSConn)
	}),
	"GetEbsEncryptionByDefault": ec2Handler(func(input *ec2.GetEbsEncryptionByDefaultInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_account.GetEbsEncryptionByDefault(input, gw.NATSConn)
	}),
	"GetSerialConsoleAccessStatus": ec2Handler(func(input *ec2.GetSerialConsoleAccessStatusInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_account.GetSerialConsoleAccessStatus(input, gw.NATSConn)
	}),
	"EnableSerialConsoleAccess": ec2Handler(func(input *ec2.EnableSerialConsoleAccessInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_account.EnableSerialConsoleAccess(input, gw.NATSConn)
	}),
	"DisableSerialConsoleAccess": ec2Handler(func(input *ec2.DisableSerialConsoleAccessInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_account.DisableSerialConsoleAccess(input, gw.NATSConn)
	}),
	"CreateTags": ec2Handler(func(input *ec2.CreateTagsInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_tags.CreateTags(input, gw.NATSConn)
	}),
	"DeleteTags": ec2Handler(func(input *ec2.DeleteTagsInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_tags.DeleteTags(input, gw.NATSConn)
	}),
	"DescribeTags": ec2Handler(func(input *ec2.DescribeTagsInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_tags.DescribeTags(input, gw.NATSConn)
	}),
	"CreateSnapshot": ec2Handler(func(input *ec2.CreateSnapshotInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_snapshot.CreateSnapshot(input, gw.NATSConn)
	}),
	"DeleteSnapshot": ec2Handler(func(input *ec2.DeleteSnapshotInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_snapshot.DeleteSnapshot(input, gw.NATSConn)
	}),
	"DescribeSnapshots": ec2Handler(func(input *ec2.DescribeSnapshotsInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_snapshot.DescribeSnapshots(input, gw.NATSConn)
	}),
	"CopySnapshot": ec2Handler(func(input *ec2.CopySnapshotInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_snapshot.CopySnapshot(input, gw.NATSConn)
	}),
	"CreateInternetGateway": ec2Handler(func(input *ec2.CreateInternetGatewayInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_igw.CreateInternetGateway(input, gw.NATSConn)
	}),
	"DeleteInternetGateway": ec2Handler(func(input *ec2.DeleteInternetGatewayInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_igw.DeleteInternetGateway(input, gw.NATSConn)
	}),
	"DescribeInternetGateways": ec2Handler(func(input *ec2.DescribeInternetGatewaysInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_igw.DescribeInternetGateways(input, gw.NATSConn)
	}),
	"AttachInternetGateway": ec2Handler(func(input *ec2.AttachInternetGatewayInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_igw.AttachInternetGateway(input, gw.NATSConn)
	}),
	"DetachInternetGateway": ec2Handler(func(input *ec2.DetachInternetGatewayInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_igw.DetachInternetGateway(input, gw.NATSConn)
	}),
	"CreateEgressOnlyInternetGateway": ec2Handler(func(input *ec2.CreateEgressOnlyInternetGatewayInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_eigw.CreateEgressOnlyInternetGateway(input, gw.NATSConn)
	}),
	"DeleteEgressOnlyInternetGateway": ec2Handler(func(input *ec2.DeleteEgressOnlyInternetGatewayInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_eigw.DeleteEgressOnlyInternetGateway(input, gw.NATSConn)
	}),
	"DescribeEgressOnlyInternetGateways": ec2Handler(func(input *ec2.DescribeEgressOnlyInternetGatewaysInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_eigw.DescribeEgressOnlyInternetGateways(input, gw.NATSConn)
	}),
	"CreateVpc": ec2Handler(func(input *ec2.CreateVpcInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_vpc.CreateVpc(input, gw.NATSConn)
	}),
	"DeleteVpc": ec2Handler(func(input *ec2.DeleteVpcInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_vpc.DeleteVpc(input, gw.NATSConn)
	}),
	"DescribeVpcs": ec2Handler(func(input *ec2.DescribeVpcsInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_vpc.DescribeVpcs(input, gw.NATSConn)
	}),
	"CreateSubnet": ec2Handler(func(input *ec2.CreateSubnetInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_vpc.CreateSubnet(input, gw.NATSConn)
	}),
	"DeleteSubnet": ec2Handler(func(input *ec2.DeleteSubnetInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_vpc.DeleteSubnet(input, gw.NATSConn)
	}),
	"DescribeSubnets": ec2Handler(func(input *ec2.DescribeSubnetsInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_vpc.DescribeSubnets(input, gw.NATSConn)
	}),
	"CreateNetworkInterface": ec2Handler(func(input *ec2.CreateNetworkInterfaceInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_vpc.CreateNetworkInterface(input, gw.NATSConn)
	}),
	"DeleteNetworkInterface": ec2Handler(func(input *ec2.DeleteNetworkInterfaceInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_vpc.DeleteNetworkInterface(input, gw.NATSConn)
	}),
	"DescribeNetworkInterfaces": ec2Handler(func(input *ec2.DescribeNetworkInterfacesInput, gw *GatewayConfig) (any, error) {
		return gateway_ec2_vpc.DescribeNetworkInterfaces(input, gw.NATSConn)
	}),
}

// ec2LocalActions are actions that don't require a NATS connection.
var ec2LocalActions = map[string]bool{
	"DescribeRegions":           true,
	"DescribeAvailabilityZones": true,
	"DescribeAccountAttributes": true,
}

func (gw *GatewayConfig) EC2_Request(ctx *fiber.Ctx) error {
	queryArgs := ParseAWSQueryArgs(string(ctx.Body()))

	action := queryArgs["Action"]
	handler, ok := ec2Actions[action]
	if !ok {
		return errors.New(awserrors.ErrorInvalidAction)
	}

	if err := gw.checkPolicy(ctx, "ec2", action); err != nil {
		return err
	}

	if gw.NATSConn == nil && !ec2LocalActions[action] {
		return errors.New(awserrors.ErrorServerInternal)
	}

	xmlOutput, err := handler(action, queryArgs, gw)
	if err != nil {
		return err
	}

	return ctx.Status(fiber.StatusOK).Type("text/xml").Send(xmlOutput)
}
