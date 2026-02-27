package gateway_iam

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_iam "github.com/mulgadc/hive/hive/handlers/iam"
)

func CreatePolicy(input *iam.CreatePolicyInput, svc handlers_iam.IAMService) (*iam.CreatePolicyOutput, error) {
	if input.PolicyName == nil || *input.PolicyName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.PolicyDocument == nil || *input.PolicyDocument == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.CreatePolicy(input)
}

func GetPolicy(input *iam.GetPolicyInput, svc handlers_iam.IAMService) (*iam.GetPolicyOutput, error) {
	if input.PolicyArn == nil || *input.PolicyArn == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.GetPolicy(input)
}

func GetPolicyVersion(input *iam.GetPolicyVersionInput, svc handlers_iam.IAMService) (*iam.GetPolicyVersionOutput, error) {
	if input.PolicyArn == nil || *input.PolicyArn == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.VersionId == nil || *input.VersionId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.GetPolicyVersion(input)
}

func ListPolicies(input *iam.ListPoliciesInput, svc handlers_iam.IAMService) (*iam.ListPoliciesOutput, error) {
	return svc.ListPolicies(input)
}

func DeletePolicy(input *iam.DeletePolicyInput, svc handlers_iam.IAMService) (*iam.DeletePolicyOutput, error) {
	if input.PolicyArn == nil || *input.PolicyArn == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.DeletePolicy(input)
}

func AttachUserPolicy(input *iam.AttachUserPolicyInput, svc handlers_iam.IAMService) (*iam.AttachUserPolicyOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.PolicyArn == nil || *input.PolicyArn == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.AttachUserPolicy(input)
}

func DetachUserPolicy(input *iam.DetachUserPolicyInput, svc handlers_iam.IAMService) (*iam.DetachUserPolicyOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.PolicyArn == nil || *input.PolicyArn == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.DetachUserPolicy(input)
}

func ListAttachedUserPolicies(input *iam.ListAttachedUserPoliciesInput, svc handlers_iam.IAMService) (*iam.ListAttachedUserPoliciesOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.ListAttachedUserPolicies(input)
}
