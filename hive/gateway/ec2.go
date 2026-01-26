package gateway

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/gofiber/fiber/v2"
	"github.com/mulgadc/hive/hive/awsec2query"
	gateway_ec2_image "github.com/mulgadc/hive/hive/gateway/ec2/image"
	gateway_ec2_instance "github.com/mulgadc/hive/hive/gateway/ec2/instance"
	gateway_ec2_key "github.com/mulgadc/hive/hive/gateway/ec2/key"
	gateway_ec2_regions "github.com/mulgadc/hive/hive/gateway/ec2/regions"
	gateway_ec2_volume "github.com/mulgadc/hive/hive/gateway/ec2/volume"
	"github.com/mulgadc/hive/hive/utils"
)

func (gw *GatewayConfig) EC2_Request(ctx *fiber.Ctx) error {

	queryArgs := ParseAWSQueryArgs(string(ctx.Body()))

	var xmlOutput []byte
	var err error

	// Run the action
	// TODO: Generate for each action, unit test each, and invalid action
	switch queryArgs["Action"] {

	case "DescribeInstances":
		var input = &ec2.DescribeInstancesInput{}
		err = awsec2query.QueryParamsToStruct(queryArgs, input)

		if err != nil {
			return err
		}

		// Dynamically discover active nodes instead of using static config value
		activeNodes := gw.DiscoverActiveNodes()
		output, err := gateway_ec2_instance.DescribeInstances(input, gw.NATSConn, activeNodes)

		if err != nil {
			return err
		}

		// Convert to XML
		payload := utils.GenerateXMLPayload("DescribeInstancesResponse", output)
		xmlOutput, err = utils.MarshalToXML(payload)

		if err != nil {
			return errors.New("failed to marshal response to XML")
		}
	case "RunInstances":

		var input = &ec2.RunInstancesInput{}
		err = awsec2query.QueryParamsToStruct(queryArgs, input)

		if err != nil {
			return err
		}

		output, err := gateway_ec2_instance.RunInstances(input, gw.NATSConn)

		if err != nil {
			return err
		}

		// Convert to XML
		payload := utils.GenerateXMLPayload("RunInstancesResponse", output)
		xmlOutput, err = utils.MarshalToXML(payload)

		if err != nil {
			return errors.New("failed to marshal response to XML")
		}

	case "StartInstances":

		var input = &ec2.StartInstancesInput{}
		err = awsec2query.QueryParamsToStruct(queryArgs, input)

		if err != nil {
			return err
		}

		output, err := gateway_ec2_instance.StartInstances(input, gw.NATSConn)

		if err != nil {
			return err
		}

		// Convert to XML
		payload := utils.GenerateXMLPayload("StartInstancesResponse", output)
		xmlOutput, err = utils.MarshalToXML(payload)

		if err != nil {
			return errors.New("failed to marshal response to XML")
		}

	case "StopInstances":

		var input = &ec2.StopInstancesInput{}
		err = awsec2query.QueryParamsToStruct(queryArgs, input)

		if err != nil {
			return err
		}

		output, err := gateway_ec2_instance.StopInstances(input, gw.NATSConn)

		if err != nil {
			return err
		}

		// Convert to XML
		payload := utils.GenerateXMLPayload("StopInstancesResponse", output)
		xmlOutput, err = utils.MarshalToXML(payload)

		if err != nil {
			return errors.New("failed to marshal response to XML")
		}

	case "TerminateInstances":

		var input = &ec2.TerminateInstancesInput{}
		err = awsec2query.QueryParamsToStruct(queryArgs, input)

		if err != nil {
			return err
		}

		output, err := gateway_ec2_instance.TerminateInstances(input, gw.NATSConn)

		if err != nil {
			return err
		}

		// Convert to XML
		payload := utils.GenerateXMLPayload("TerminateInstancesResponse", output)
		xmlOutput, err = utils.MarshalToXML(payload)

		if err != nil {
			return errors.New("failed to marshal response to XML")
		}

	case "DescribeInstanceTypes":
		var input = &ec2.DescribeInstanceTypesInput{}
		err = awsec2query.QueryParamsToStruct(queryArgs, input)

		if err != nil {
			return err
		}

		output, err := gateway_ec2_instance.DescribeInstanceTypes(input, gw.NATSConn, gw.ExpectedNodes)

		if err != nil {
			return err
		}

		// Convert to XML
		payload := utils.GenerateXMLPayload("DescribeInstanceTypesResponse", output)
		xmlOutput, err = utils.MarshalToXML(payload)

		if err != nil {
			return errors.New("failed to marshal response to XML")
		}

	case "CreateKeyPair":

		var input = &ec2.CreateKeyPairInput{}
		err = awsec2query.QueryParamsToStruct(queryArgs, input)

		if err != nil {
			return err
		}

		output, err := gateway_ec2_key.CreateKeyPair(input, gw.NATSConn)

		if err != nil {
			return err
		}

		// Convert to XML
		payload := utils.GenerateXMLPayload("CreateKeyPairResponse", output)
		xmlOutput, err = utils.MarshalToXML(payload)

		if err != nil {
			return errors.New("failed to marshal response to XML")
		}

	case "DeleteKeyPair":

		var input = &ec2.DeleteKeyPairInput{}
		err = awsec2query.QueryParamsToStruct(queryArgs, input)

		if err != nil {
			return err
		}

		output, err := gateway_ec2_key.DeleteKeyPair(input, gw.NATSConn)

		if err != nil {
			return err
		}

		// Convert to XML
		payload := utils.GenerateXMLPayload("DeleteKeyPairResponse", output)
		xmlOutput, err = utils.MarshalToXML(payload)

		if err != nil {
			return errors.New("failed to marshal response to XML")
		}

	case "DescribeKeyPairs":

		var input = &ec2.DescribeKeyPairsInput{}
		err = awsec2query.QueryParamsToStruct(queryArgs, input)

		if err != nil {
			return err
		}

		output, err := gateway_ec2_key.DescribeKeyPairs(input, gw.NATSConn)

		if err != nil {
			return err
		}

		// Convert to XML
		payload := utils.GenerateXMLPayload("DescribeKeyPairsResponse", output)
		xmlOutput, err = utils.MarshalToXML(payload)

		if err != nil {
			return errors.New("failed to marshal response to XML")
		}

	case "ImportKeyPair":

		var input = &ec2.ImportKeyPairInput{}

		// Bug in parser, end of Base64 is URL encoded
		if strings.HasSuffix(queryArgs["PublicKeyMaterial"], "%3D%3D") {
			queryArgs["PublicKeyMaterial"] = strings.Replace(queryArgs["PublicKeyMaterial"], "%3D%3D", "==", 1)
		}

		err = awsec2query.QueryParamsToStruct(queryArgs, input)

		if err != nil {
			return err
		}

		output, err := gateway_ec2_key.ImportKeyPair(input, gw.NATSConn)

		if err != nil {
			return err
		}

		// Convert to XML
		payload := utils.GenerateXMLPayload("ImportKeyPairResponse", output)
		xmlOutput, err = utils.MarshalToXML(payload)

		if err != nil {
			return errors.New("failed to marshal response to XML")
		}

	case "DescribeImages":

		var input = &ec2.DescribeImagesInput{}
		err = awsec2query.QueryParamsToStruct(queryArgs, input)

		if err != nil {
			return err
		}

		output, err := gateway_ec2_image.DescribeImages(input, gw.NATSConn)

		if err != nil {
			return err
		}

		// Convert to XML
		payload := utils.GenerateXMLPayload("DescribeImagesResponse", output)
		xmlOutput, err = utils.MarshalToXML(payload)

		if err != nil {
			return errors.New("failed to marshal response to XML")
		}
	case "DescribeRegions":
		var input = &ec2.DescribeRegionsInput{}
		err = awsec2query.QueryParamsToStruct(queryArgs, input)

		if err != nil {
			return err
		}

		output, err := gateway_ec2_regions.DescribeRegions(input, gw.Region)

		if err != nil {
			return err
		}

		// Convert to XML
		payload := utils.GenerateXMLPayload("DescribeRegionsResponse", output)
		xmlOutput, err = utils.MarshalToXML(payload)

		if err != nil {
			return errors.New("failed to marshal response to XML")
		}

	case "DescribeVolumes":
		var input = &ec2.DescribeVolumesInput{}
		err = awsec2query.QueryParamsToStruct(queryArgs, input)

		if err != nil {
			return err
		}

		output, err := gateway_ec2_volume.DescribeVolumes(input, gw.NATSConn)

		if err != nil {
			return err
		}

		// Convert to XML
		payload := utils.GenerateXMLPayload("DescribeVolumesResponse", output)
		xmlOutput, err = utils.MarshalToXML(payload)

		if err != nil {
			return errors.New("failed to marshal response to XML")
		}

	default:
		err = errors.New("InvalidAction")
	}

	// Return an error XML
	if err != nil {
		return err
	}

	ctx.Status(fiber.StatusOK).Type("text/xml").Send(xmlOutput)

	return nil

}
