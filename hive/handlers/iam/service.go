package handlers_iam

import (
	"github.com/aws/aws-sdk-go/service/iam"
)

// IAMService defines the interface for IAM operations.
type IAMService interface {
	// User CRUD — account-scoped
	CreateUser(accountID string, input *iam.CreateUserInput) (*iam.CreateUserOutput, error)
	GetUser(accountID string, input *iam.GetUserInput) (*iam.GetUserOutput, error)
	ListUsers(accountID string, input *iam.ListUsersInput) (*iam.ListUsersOutput, error)
	DeleteUser(accountID string, input *iam.DeleteUserInput) (*iam.DeleteUserOutput, error)

	// Access key lifecycle — account-scoped
	CreateAccessKey(accountID string, input *iam.CreateAccessKeyInput) (*iam.CreateAccessKeyOutput, error)
	ListAccessKeys(accountID string, input *iam.ListAccessKeysInput) (*iam.ListAccessKeysOutput, error)
	DeleteAccessKey(accountID string, input *iam.DeleteAccessKeyInput) (*iam.DeleteAccessKeyOutput, error)
	UpdateAccessKey(input *iam.UpdateAccessKeyInput) (*iam.UpdateAccessKeyOutput, error) // key ID is globally unique

	// Policy CRUD — account-scoped
	CreatePolicy(accountID string, input *iam.CreatePolicyInput) (*iam.CreatePolicyOutput, error)
	GetPolicy(accountID string, input *iam.GetPolicyInput) (*iam.GetPolicyOutput, error)
	GetPolicyVersion(accountID string, input *iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error)
	ListPolicies(accountID string, input *iam.ListPoliciesInput) (*iam.ListPoliciesOutput, error)
	DeletePolicy(accountID string, input *iam.DeletePolicyInput) (*iam.DeletePolicyOutput, error)

	// Policy attachment — account-scoped
	AttachUserPolicy(accountID string, input *iam.AttachUserPolicyInput) (*iam.AttachUserPolicyOutput, error)
	DetachUserPolicy(accountID string, input *iam.DetachUserPolicyInput) (*iam.DetachUserPolicyOutput, error)
	ListAttachedUserPolicies(accountID string, input *iam.ListAttachedUserPoliciesInput) (*iam.ListAttachedUserPoliciesOutput, error)

	// Policy evaluation (internal — used by gateway enforcement)
	GetUserPolicies(accountID, userName string) ([]PolicyDocument, error)

	// Auth (internal — used by SigV4 middleware and bootstrap, not exposed via gateway)
	LookupAccessKey(accessKeyID string) (*AccessKey, error)
	SeedRootUser(data *BootstrapData) error
	IsEmpty() (bool, error)

	// Account operations
	CreateAccount(name string) (*Account, error)
	GetAccount(accountID string) (*Account, error)
	ListAccounts() ([]*Account, error)
}
