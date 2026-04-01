package utils

import (
	"github.com/aws/aws-sdk-go/service/ec2"
)

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
