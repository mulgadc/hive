package gateway

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/gofiber/fiber/v2"
	"github.com/mulgadc/hive/hive/awsec2query"
	gateway_ec2_image "github.com/mulgadc/hive/hive/gateway/ec2/image"
	gateway_ec2_instance "github.com/mulgadc/hive/hive/gateway/ec2/instance"
	gateway_ec2_key "github.com/mulgadc/hive/hive/gateway/ec2/key"
	"github.com/mulgadc/hive/hive/utils"
)

func (gw *GatewayConfig) EC2_Request(ctx *fiber.Ctx) error {

	queryArgs := ParseAWSQueryArgs(string(ctx.Body()))

	var xmlOutput []byte
	var err error

	// Run the action
	// TODO: Generate for each action, unit test each, and invalid action
	switch queryArgs["Action"] {

	//case "DescribeInstances":
	//	xmlOutput, err = gateway_ec2_instance.DescribeInstances(ctx, queryArgs)
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
		payload := utils.GenerateXMLPayload("RunInstanceResponse", output)
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

/*
func (gw *GatewayConfig) EC2_DescribeInstances(ctx *fiber.Ctx, args map[string]string) error {

	slog.Info("EC2 DescribeInstances called")

	// Return a dummy response
	response := `<?xml version="1.0" encoding="UTF-8"?>
<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/">
  <requestId>2b7ac2f1-9acd-4d73-b0a1-1b2e5607f9ab</requestId>
  <reservationSet>
    <item>
      <reservationId>r-0f6b3c4d5e6789abc</reservationId>
      <ownerId>123456789012</ownerId>
      <groupSet/>
      <instancesSet>
        <item>
          <instanceId>i-0123456789abcdef0</instanceId>
          <imageId>ami-08d4ac5b634553e16</imageId>
          <instanceState>
            <code>16</code>
            <name>running</name>
          </instanceState>
          <privateDnsName>ip-10-0-1-25.ap-southeast-2.compute.internal</privateDnsName>
          <dnsName>ec2-3-26-14-112.ap-southeast-2.compute.amazonaws.com</dnsName>
          <reason/>
          <keyName>my-keypair</keyName>
          <amiLaunchIndex>0</amiLaunchIndex>
          <productCodes/>
          <instanceType>t3.medium</instanceType>
          <launchTime>2025-02-18T04:12:07.000Z</launchTime>
          <placement>
            <availabilityZone>ap-southeast-2a</availabilityZone>
            <groupName/>
            <tenancy>default</tenancy>
          </placement>
          <monitoring>
            <state>disabled</state>
          </monitoring>
          <privateIpAddress>10.0.1.25</privateIpAddress>
          <ipAddress>3.26.14.112</ipAddress>
          <subnetId>subnet-0ab1c2d3e4f567890</subnetId>
          <vpcId>vpc-0123abcd4567efgh8</vpcId>
          <sourceDestCheck>true</sourceDestCheck>
          <groupSet>
            <item>
              <groupId>sg-0abc1234def567890</groupId>
              <groupName>default</groupName>
            </item>
          </groupSet>
          <stateReason/>
          <architecture>x86_64</architecture>
          <rootDeviceType>ebs</rootDeviceType>
          <rootDeviceName>/dev/xvda</rootDeviceName>
          <blockDeviceMapping>
            <item>
              <deviceName>/dev/xvda</deviceName>
              <ebs>
                <volumeId>vol-06f5e4d3c2b1a0f98</volumeId>
                <status>attached</status>
                <attachTime>2025-02-18T04:12:09.000Z</attachTime>
                <deleteOnTermination>true</deleteOnTermination>
              </ebs>
            </item>
          </blockDeviceMapping>
          <virtualizationType>hvm</virtualizationType>
          <tagSet>
            <item>
              <key>Name</key>
              <value>web-01</value>
            </item>
          </tagSet>
          <hypervisor>xen</hypervisor>
          <networkInterfaceSet>
            <item>
              <networkInterfaceId>eni-0a12b34c56d78ef90</networkInterfaceId>
              <subnetId>subnet-0ab1c2d3e4f567890</subnetId>
              <vpcId>vpc-0123abcd4567efgh8</vpcId>
              <description/>
              <ownerId>123456789012</ownerId>
              <status>in-use</status>
              <macAddress>0a:1b:2c:3d:4e:5f</macAddress>
              <privateIpAddress>10.0.1.25</privateIpAddress>
              <privateDnsName>ip-10-0-1-25.ap-southeast-2.compute.internal</privateDnsName>
              <sourceDestCheck>true</sourceDestCheck>
              <groupSet>
                <item>
                  <groupId>sg-0abc1234def567890</groupId>
                  <groupName>default</groupName>
                </item>
              </groupSet>
              <attachment>
                <attachmentId>eni-attach-0abc123def4567890</attachmentId>
                <deviceIndex>0</deviceIndex>
                <status>attached</status>
                <attachTime>2025-02-18T04:12:07.000Z</attachTime>
                <deleteOnTermination>true</deleteOnTermination>
              </attachment>
              <privateIpAddressesSet>
                <item>
                  <privateIpAddress>10.0.1.25</privateIpAddress>
                  <privateDnsName>ip-10-0-1-25.ap-southeast-2.compute.internal</privateDnsName>
                  <primary>true</primary>
                </item>
              </privateIpAddressesSet>
            </item>
          </networkInterfaceSet>
        </item>
      </instancesSet>
    </item>
  </reservationSet>
</DescribeInstancesResponse>
`

	return ctx.SendString(response)
}

*/
