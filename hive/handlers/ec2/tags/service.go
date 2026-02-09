package handlers_ec2_tags

import "github.com/aws/aws-sdk-go/service/ec2"

// TagsService defines the interface for EC2 tag operations
type TagsService interface {
	CreateTags(input *ec2.CreateTagsInput) (*ec2.CreateTagsOutput, error)
	DescribeTags(input *ec2.DescribeTagsInput) (*ec2.DescribeTagsOutput, error)
	DeleteTags(input *ec2.DeleteTagsInput) (*ec2.DeleteTagsOutput, error)
}
