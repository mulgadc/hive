package utils

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

// MapToEC2Tags converts a map[string]string to a slice of EC2 Tag pointers.
// Returns nil when the input map is empty.
func MapToEC2Tags(m map[string]string) []*ec2.Tag {
	if len(m) == 0 {
		return nil
	}
	tags := make([]*ec2.Tag, 0, len(m))
	for k, v := range m {
		tags = append(tags, &ec2.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return tags
}

// ExtractTags returns a map of tags from the TagSpecification that matches
// the given resourceType. If no specification matches, the returned map is
// empty (never nil).
func ExtractTags(tagSpecs []*ec2.TagSpecification, resourceType string) map[string]string {
	tags := make(map[string]string)
	for _, tagSpec := range tagSpecs {
		if tagSpec.ResourceType != nil && *tagSpec.ResourceType == resourceType {
			for _, tag := range tagSpec.Tags {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = *tag.Value
				}
			}
		}
	}
	return tags
}
