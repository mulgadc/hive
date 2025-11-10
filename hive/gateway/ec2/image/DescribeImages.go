package gateway_ec2_image

import (
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/handlers/ec2/image"
	"github.com/nats-io/nats.go"
)

/*
Sample JSON:

    "DescribeImages":{
      "name":"DescribeImages",
      "http":{
        "method":"POST",
        "requestUri":"/"
      },
      "input":{"shape":"DescribeImagesRequest"},
      "output":{"shape":"DescribeImagesResult"}
    },

    "DescribeImagesRequest":{
      "type":"structure",
      "members":{
        "ExecutableUsers":{
          "shape":"ExecutableByStringList",
          "locationName":"ExecutableBy"
        },
        "Filters":{
          "shape":"FilterList",
          "locationName":"Filter"
        },
        "ImageIds":{
          "shape":"ImageIdStringList",
          "locationName":"ImageId"
        },
        "Owners":{
          "shape":"OwnerStringList",
          "locationName":"Owner"
        },
        "IncludeDeprecated":{"shape":"Boolean"},
        "IncludeDisabled":{"shape":"Boolean"},
        "DryRun":{
          "shape":"Boolean",
          "locationName":"dryRun"
        },
        "MaxResults":{"shape":"Integer"},
        "NextToken":{"shape":"String"}
      }
    },

    "DescribeImagesResult":{
      "type":"structure",
      "members":{
        "Images":{
          "shape":"ImageList",
          "locationName":"imagesSet"
        },
        "NextToken":{
          "shape":"String",
          "locationName":"nextToken"
        }
      }
    }
*/

func ValidateDescribeImagesInput(input *ec2.DescribeImagesInput) (err error) {

	// No required fields for DescribeImages
	// All parameters are optional

	if input == nil {
		return nil
	}

	// Validate ImageId format if provided
	if input.ImageIds != nil {
		for _, imageId := range input.ImageIds {
			if imageId != nil && !strings.HasPrefix(*imageId, "ami-") {
				return errors.New("InvalidAMIID.Malformed")
			}
		}
	}

	return
}

func DescribeImages(input *ec2.DescribeImagesInput, natsConn *nats.Conn) (output ec2.DescribeImagesOutput, err error) {

	// Validate input
	err = ValidateDescribeImagesInput(input)

	if err != nil {
		return output, err
	}

	// Create NATS service and call handler
	imageService := handlers_ec2_image.NewNATSImageService(natsConn)
	result, err := imageService.DescribeImages(input)

	if err != nil {
		return output, err
	}

	// Return result
	output = *result
	return output, nil
}
