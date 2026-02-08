package gateway

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/gofiber/fiber/v2"
	"github.com/mulgadc/hive/hive/awsec2query"
	gateway_ec2_account "github.com/mulgadc/hive/hive/gateway/ec2/account"
	gateway_ec2_image "github.com/mulgadc/hive/hive/gateway/ec2/image"
	gateway_ec2_instance "github.com/mulgadc/hive/hive/gateway/ec2/instance"
	gateway_ec2_key "github.com/mulgadc/hive/hive/gateway/ec2/key"
	gateway_ec2_volume "github.com/mulgadc/hive/hive/gateway/ec2/volume"
	gateway_ec2_zone "github.com/mulgadc/hive/hive/gateway/ec2/zone"
	"github.com/mulgadc/hive/hive/utils"
)

// EC2Handler processes parsed query args and returns XML response bytes.
type EC2Handler func(q map[string]string, gw *GatewayConfig) ([]byte, error)

// ec2Response parses query args into the input struct, calls the handler,
// and marshals the result to an XML response.
func ec2Response(action string, q map[string]string, input any, call func() (any, error)) ([]byte, error) {
	if err := awsec2query.QueryParamsToStruct(q, input); err != nil {
		return nil, err
	}
	output, err := call()
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

var ec2Actions = map[string]EC2Handler{
	"DescribeInstances": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.DescribeInstancesInput{}
		return ec2Response("DescribeInstances", q, input, func() (any, error) {
			return gateway_ec2_instance.DescribeInstances(input, gw.NATSConn, gw.DiscoverActiveNodes())
		})
	},
	"RunInstances": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.RunInstancesInput{}
		return ec2Response("RunInstances", q, input, func() (any, error) {
			return gateway_ec2_instance.RunInstances(input, gw.NATSConn)
		})
	},
	"StartInstances": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.StartInstancesInput{}
		return ec2Response("StartInstances", q, input, func() (any, error) {
			return gateway_ec2_instance.StartInstances(input, gw.NATSConn)
		})
	},
	"StopInstances": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.StopInstancesInput{}
		return ec2Response("StopInstances", q, input, func() (any, error) {
			return gateway_ec2_instance.StopInstances(input, gw.NATSConn)
		})
	},
	"TerminateInstances": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.TerminateInstancesInput{}
		return ec2Response("TerminateInstances", q, input, func() (any, error) {
			return gateway_ec2_instance.TerminateInstances(input, gw.NATSConn)
		})
	},
	"DescribeInstanceTypes": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.DescribeInstanceTypesInput{}
		return ec2Response("DescribeInstanceTypes", q, input, func() (any, error) {
			return gateway_ec2_instance.DescribeInstanceTypes(input, gw.NATSConn, gw.ExpectedNodes)
		})
	},
	"CreateKeyPair": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.CreateKeyPairInput{}
		return ec2Response("CreateKeyPair", q, input, func() (any, error) {
			return gateway_ec2_key.CreateKeyPair(input, gw.NATSConn)
		})
	},
	"DeleteKeyPair": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.DeleteKeyPairInput{}
		return ec2Response("DeleteKeyPair", q, input, func() (any, error) {
			return gateway_ec2_key.DeleteKeyPair(input, gw.NATSConn)
		})
	},
	"DescribeKeyPairs": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.DescribeKeyPairsInput{}
		return ec2Response("DescribeKeyPairs", q, input, func() (any, error) {
			return gateway_ec2_key.DescribeKeyPairs(input, gw.NATSConn)
		})
	},
	"ImportKeyPair": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		// Bug in parser, end of Base64 is URL encoded
		if strings.HasSuffix(q["PublicKeyMaterial"], "%3D%3D") {
			q["PublicKeyMaterial"] = strings.Replace(q["PublicKeyMaterial"], "%3D%3D", "==", 1)
		}
		input := &ec2.ImportKeyPairInput{}
		return ec2Response("ImportKeyPair", q, input, func() (any, error) {
			return gateway_ec2_key.ImportKeyPair(input, gw.NATSConn)
		})
	},
	"DescribeImages": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.DescribeImagesInput{}
		return ec2Response("DescribeImages", q, input, func() (any, error) {
			return gateway_ec2_image.DescribeImages(input, gw.NATSConn)
		})
	},
	"DescribeRegions": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.DescribeRegionsInput{}
		return ec2Response("DescribeRegions", q, input, func() (any, error) {
			return gateway_ec2_zone.DescribeRegions(input, gw.Region)
		})
	},
	"DescribeAvailabilityZones": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.DescribeAvailabilityZonesInput{}
		return ec2Response("DescribeAvailabilityZones", q, input, func() (any, error) {
			return gateway_ec2_zone.DescribeAvailabilityZones(input, gw.Region, gw.AZ)
		})
	},
	"DescribeVolumes": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.DescribeVolumesInput{}
		return ec2Response("DescribeVolumes", q, input, func() (any, error) {
			return gateway_ec2_volume.DescribeVolumes(input, gw.NATSConn)
		})
	},
	"ModifyVolume": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.ModifyVolumeInput{}
		return ec2Response("ModifyVolume", q, input, func() (any, error) {
			return gateway_ec2_volume.ModifyVolume(input, gw.NATSConn)
		})
	},
	"CreateVolume": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.CreateVolumeInput{}
		return ec2Response("CreateVolume", q, input, func() (any, error) {
			return gateway_ec2_volume.CreateVolume(input, gw.NATSConn)
		})
	},
	"DeleteVolume": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.DeleteVolumeInput{}
		return ec2Response("DeleteVolume", q, input, func() (any, error) {
			return gateway_ec2_volume.DeleteVolume(input, gw.NATSConn)
		})
	},
	"AttachVolume": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.AttachVolumeInput{}
		return ec2Response("AttachVolume", q, input, func() (any, error) {
			return gateway_ec2_volume.AttachVolume(input, gw.NATSConn)
		})
	},
	"DescribeVolumeStatus": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.DescribeVolumeStatusInput{}
		return ec2Response("DescribeVolumeStatus", q, input, func() (any, error) {
			return gateway_ec2_volume.DescribeVolumeStatus(input, gw.NATSConn)
		})
	},
	"DetachVolume": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.DetachVolumeInput{}
		return ec2Response("DetachVolume", q, input, func() (any, error) {
			return gateway_ec2_volume.DetachVolume(input, gw.NATSConn)
		})
	},
	"DescribeAccountAttributes": func(q map[string]string, gw *GatewayConfig) ([]byte, error) {
		input := &ec2.DescribeAccountAttributesInput{}
		return ec2Response("DescribeAccountAttributes", q, input, func() (any, error) {
			return gateway_ec2_account.DescribeAccountAttributes(input)
		})
	},
}

func (gw *GatewayConfig) EC2_Request(ctx *fiber.Ctx) error {
	queryArgs := ParseAWSQueryArgs(string(ctx.Body()))

	action := queryArgs["Action"]
	handler, ok := ec2Actions[action]
	if !ok {
		return errors.New("InvalidAction")
	}

	xmlOutput, err := handler(queryArgs, gw)
	if err != nil {
		return err
	}

	ctx.Status(fiber.StatusOK).Type("text/xml").Send(xmlOutput)
	return nil
}
