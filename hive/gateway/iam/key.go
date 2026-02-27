package gateway_iam

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_iam "github.com/mulgadc/hive/hive/handlers/iam"
)

func CreateAccessKey(accountID string, input *iam.CreateAccessKeyInput, svc handlers_iam.IAMService) (*iam.CreateAccessKeyOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.CreateAccessKey(accountID, input)
}

func ListAccessKeys(accountID string, input *iam.ListAccessKeysInput, svc handlers_iam.IAMService) (*iam.ListAccessKeysOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.ListAccessKeys(accountID, input)
}

func DeleteAccessKey(accountID string, input *iam.DeleteAccessKeyInput, svc handlers_iam.IAMService) (*iam.DeleteAccessKeyOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.AccessKeyId == nil || *input.AccessKeyId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.DeleteAccessKey(accountID, input)
}

func UpdateAccessKey(input *iam.UpdateAccessKeyInput, svc handlers_iam.IAMService) (*iam.UpdateAccessKeyOutput, error) {
	if input.AccessKeyId == nil || *input.AccessKeyId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.Status == nil || *input.Status == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.UpdateAccessKey(input)
}
