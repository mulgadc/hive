package handlers_iam

import (
	"github.com/aws/aws-sdk-go/service/iam"
)

// IAMService defines the interface for IAM operations.
type IAMService interface {
	// User CRUD
	CreateUser(input *iam.CreateUserInput) (*iam.CreateUserOutput, error)
	GetUser(input *iam.GetUserInput) (*iam.GetUserOutput, error)
	ListUsers(input *iam.ListUsersInput) (*iam.ListUsersOutput, error)
	DeleteUser(input *iam.DeleteUserInput) (*iam.DeleteUserOutput, error)

	// Access key lifecycle
	CreateAccessKey(input *iam.CreateAccessKeyInput) (*iam.CreateAccessKeyOutput, error)
	ListAccessKeys(input *iam.ListAccessKeysInput) (*iam.ListAccessKeysOutput, error)
	DeleteAccessKey(input *iam.DeleteAccessKeyInput) (*iam.DeleteAccessKeyOutput, error)
	UpdateAccessKey(input *iam.UpdateAccessKeyInput) (*iam.UpdateAccessKeyOutput, error)

	// Policy CRUD
	CreatePolicy(input *iam.CreatePolicyInput) (*iam.CreatePolicyOutput, error)
	GetPolicy(input *iam.GetPolicyInput) (*iam.GetPolicyOutput, error)
	GetPolicyVersion(input *iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error)
	ListPolicies(input *iam.ListPoliciesInput) (*iam.ListPoliciesOutput, error)
	DeletePolicy(input *iam.DeletePolicyInput) (*iam.DeletePolicyOutput, error)

	// Policy attachment
	AttachUserPolicy(input *iam.AttachUserPolicyInput) (*iam.AttachUserPolicyOutput, error)
	DetachUserPolicy(input *iam.DetachUserPolicyInput) (*iam.DetachUserPolicyOutput, error)
	ListAttachedUserPolicies(input *iam.ListAttachedUserPoliciesInput) (*iam.ListAttachedUserPoliciesOutput, error)

	// Policy evaluation (internal — used by gateway enforcement)
	GetUserPolicies(userName string) ([]PolicyDocument, error)

	// Auth (internal — used by SigV4 middleware and bootstrap, not exposed via gateway)
	LookupAccessKey(accessKeyID string) (*AccessKey, error)
	SeedRootUser(data *BootstrapData) error
	IsEmpty() (bool, error)
}
