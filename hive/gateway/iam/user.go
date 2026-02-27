package gateway_iam

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_iam "github.com/mulgadc/hive/hive/handlers/iam"
)

func CreateUser(input *iam.CreateUserInput, svc handlers_iam.IAMService) (*iam.CreateUserOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.CreateUser(input)
}

func GetUser(input *iam.GetUserInput, svc handlers_iam.IAMService) (*iam.GetUserOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.GetUser(input)
}

func ListUsers(input *iam.ListUsersInput, svc handlers_iam.IAMService) (*iam.ListUsersOutput, error) {
	return svc.ListUsers(input)
}

func DeleteUser(input *iam.DeleteUserInput, svc handlers_iam.IAMService) (*iam.DeleteUserOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	return svc.DeleteUser(input)
}
