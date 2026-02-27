package gateway

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/gofiber/fiber/v2"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_iam "github.com/mulgadc/hive/hive/handlers/iam"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flexMockIAMService is a configurable mock with per-method overrides.
type flexMockIAMService struct {
	createUserFn      func(string, *iam.CreateUserInput) (*iam.CreateUserOutput, error)
	getUserFn         func(string, *iam.GetUserInput) (*iam.GetUserOutput, error)
	listUsersFn       func(string, *iam.ListUsersInput) (*iam.ListUsersOutput, error)
	deleteUserFn      func(string, *iam.DeleteUserInput) (*iam.DeleteUserOutput, error)
	createAccessKeyFn func(string, *iam.CreateAccessKeyInput) (*iam.CreateAccessKeyOutput, error)
	listAccessKeysFn  func(string, *iam.ListAccessKeysInput) (*iam.ListAccessKeysOutput, error)
	deleteAccessKeyFn func(string, *iam.DeleteAccessKeyInput) (*iam.DeleteAccessKeyOutput, error)
	updateAccessKeyFn func(*iam.UpdateAccessKeyInput) (*iam.UpdateAccessKeyOutput, error)
}

func (m *flexMockIAMService) CreateUser(accountID string, input *iam.CreateUserInput) (*iam.CreateUserOutput, error) {
	if m.createUserFn != nil {
		return m.createUserFn(accountID, input)
	}
	return &iam.CreateUserOutput{}, nil
}

func (m *flexMockIAMService) GetUser(accountID string, input *iam.GetUserInput) (*iam.GetUserOutput, error) {
	if m.getUserFn != nil {
		return m.getUserFn(accountID, input)
	}
	return &iam.GetUserOutput{}, nil
}

func (m *flexMockIAMService) ListUsers(accountID string, input *iam.ListUsersInput) (*iam.ListUsersOutput, error) {
	if m.listUsersFn != nil {
		return m.listUsersFn(accountID, input)
	}
	return &iam.ListUsersOutput{}, nil
}

func (m *flexMockIAMService) DeleteUser(accountID string, input *iam.DeleteUserInput) (*iam.DeleteUserOutput, error) {
	if m.deleteUserFn != nil {
		return m.deleteUserFn(accountID, input)
	}
	return &iam.DeleteUserOutput{}, nil
}

func (m *flexMockIAMService) CreateAccessKey(accountID string, input *iam.CreateAccessKeyInput) (*iam.CreateAccessKeyOutput, error) {
	if m.createAccessKeyFn != nil {
		return m.createAccessKeyFn(accountID, input)
	}
	return &iam.CreateAccessKeyOutput{}, nil
}

func (m *flexMockIAMService) ListAccessKeys(accountID string, input *iam.ListAccessKeysInput) (*iam.ListAccessKeysOutput, error) {
	if m.listAccessKeysFn != nil {
		return m.listAccessKeysFn(accountID, input)
	}
	return &iam.ListAccessKeysOutput{}, nil
}

func (m *flexMockIAMService) DeleteAccessKey(accountID string, input *iam.DeleteAccessKeyInput) (*iam.DeleteAccessKeyOutput, error) {
	if m.deleteAccessKeyFn != nil {
		return m.deleteAccessKeyFn(accountID, input)
	}
	return &iam.DeleteAccessKeyOutput{}, nil
}

func (m *flexMockIAMService) UpdateAccessKey(input *iam.UpdateAccessKeyInput) (*iam.UpdateAccessKeyOutput, error) {
	if m.updateAccessKeyFn != nil {
		return m.updateAccessKeyFn(input)
	}
	return &iam.UpdateAccessKeyOutput{}, nil
}

func (m *flexMockIAMService) CreatePolicy(_ string, _ *iam.CreatePolicyInput) (*iam.CreatePolicyOutput, error) {
	return &iam.CreatePolicyOutput{}, nil
}

func (m *flexMockIAMService) GetPolicy(_ string, _ *iam.GetPolicyInput) (*iam.GetPolicyOutput, error) {
	return &iam.GetPolicyOutput{}, nil
}

func (m *flexMockIAMService) GetPolicyVersion(_ string, _ *iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error) {
	return &iam.GetPolicyVersionOutput{}, nil
}

func (m *flexMockIAMService) ListPolicies(_ string, _ *iam.ListPoliciesInput) (*iam.ListPoliciesOutput, error) {
	return &iam.ListPoliciesOutput{}, nil
}

func (m *flexMockIAMService) DeletePolicy(_ string, _ *iam.DeletePolicyInput) (*iam.DeletePolicyOutput, error) {
	return &iam.DeletePolicyOutput{}, nil
}

func (m *flexMockIAMService) AttachUserPolicy(_ string, _ *iam.AttachUserPolicyInput) (*iam.AttachUserPolicyOutput, error) {
	return &iam.AttachUserPolicyOutput{}, nil
}

func (m *flexMockIAMService) DetachUserPolicy(_ string, _ *iam.DetachUserPolicyInput) (*iam.DetachUserPolicyOutput, error) {
	return &iam.DetachUserPolicyOutput{}, nil
}

func (m *flexMockIAMService) ListAttachedUserPolicies(_ string, _ *iam.ListAttachedUserPoliciesInput) (*iam.ListAttachedUserPoliciesOutput, error) {
	return &iam.ListAttachedUserPoliciesOutput{}, nil
}

func (m *flexMockIAMService) GetUserPolicies(_, _ string) ([]handlers_iam.PolicyDocument, error) {
	return nil, nil
}

func (m *flexMockIAMService) LookupAccessKey(_ string) (*handlers_iam.AccessKey, error) {
	return nil, errors.New("not implemented")
}

func (m *flexMockIAMService) SeedRootUser(_ *handlers_iam.BootstrapData) error { return nil }
func (m *flexMockIAMService) IsEmpty() (bool, error)                           { return true, nil }

func (m *flexMockIAMService) CreateAccount(_ string) (*handlers_iam.Account, error) {
	return nil, nil
}
func (m *flexMockIAMService) GetAccount(_ string) (*handlers_iam.Account, error) { return nil, nil }
func (m *flexMockIAMService) ListAccounts() ([]*handlers_iam.Account, error)     { return nil, nil }

// setupIAMRequestApp creates a Fiber app wired for IAM_Request testing.
func setupIAMRequestApp(svc handlers_iam.IAMService) *fiber.App {
	gw := &GatewayConfig{
		DisableLogging: true,
		IAMService:     svc,
	}
	app := fiber.New(fiber.Config{
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			return gw.ErrorHandler(ctx, err)
		},
	})
	app.Post("/", func(c *fiber.Ctx) error {
		c.Locals("sigv4.service", "iam")
		c.Locals("sigv4.accountId", "000000000000")
		return gw.IAM_Request(c)
	})
	return app
}

func TestIAMRequest_CreateUser_Success(t *testing.T) {
	svc := &flexMockIAMService{
		createUserFn: func(_ string, input *iam.CreateUserInput) (*iam.CreateUserOutput, error) {
			return &iam.CreateUserOutput{
				User: &iam.User{
					UserName: input.UserName,
					UserId:   aws.String("AIDAEXAMPLE123"),
					Arn:      aws.String("arn:aws:iam::000000000000:user/alice"),
					Path:     aws.String("/"),
				},
			}, nil
		},
	}
	app := setupIAMRequestApp(svc)

	req := httptest.NewRequest("POST", "/", strings.NewReader("Action=CreateUser&UserName=alice"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	xmlStr := string(body)
	assert.Contains(t, xmlStr, "CreateUserResult")
	assert.Contains(t, xmlStr, "alice")
}

func TestIAMRequest_ListUsers_Success(t *testing.T) {
	svc := &flexMockIAMService{
		listUsersFn: func(_ string, _ *iam.ListUsersInput) (*iam.ListUsersOutput, error) {
			return &iam.ListUsersOutput{
				Users: []*iam.User{
					{UserName: aws.String("alice"), UserId: aws.String("AID1")},
					{UserName: aws.String("bob"), UserId: aws.String("AID2")},
				},
				IsTruncated: aws.Bool(false),
			}, nil
		},
	}
	app := setupIAMRequestApp(svc)

	req := httptest.NewRequest("POST", "/", strings.NewReader("Action=ListUsers"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	xmlStr := string(body)
	assert.Contains(t, xmlStr, "ListUsersResult")
	assert.Contains(t, xmlStr, "alice")
	assert.Contains(t, xmlStr, "bob")
}

func TestIAMRequest_UnknownAction(t *testing.T) {
	app := setupIAMRequestApp(&flexMockIAMService{})

	req := httptest.NewRequest("POST", "/", strings.NewReader("Action=DoesNotExist"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "InvalidAction")
}

func TestIAMRequest_EmptyAction(t *testing.T) {
	app := setupIAMRequestApp(&flexMockIAMService{})

	req := httptest.NewRequest("POST", "/", strings.NewReader("UserName=alice"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "InvalidAction")
}

func TestIAMRequest_NilService(t *testing.T) {
	gw := &GatewayConfig{
		DisableLogging: true,
		IAMService:     nil,
	}
	app := fiber.New(fiber.Config{
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			return gw.ErrorHandler(ctx, err)
		},
	})
	app.Post("/", func(c *fiber.Ctx) error {
		c.Locals("sigv4.service", "iam")
		c.Locals("sigv4.accountId", "000000000000")
		return gw.IAM_Request(c)
	})

	req := httptest.NewRequest("POST", "/", strings.NewReader("Action=CreateUser&UserName=alice"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 500, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "InternalError")
}

func TestIAMRequest_ServiceError(t *testing.T) {
	svc := &flexMockIAMService{
		createUserFn: func(_ string, _ *iam.CreateUserInput) (*iam.CreateUserOutput, error) {
			return nil, errors.New(awserrors.ErrorIAMEntityAlreadyExists)
		},
	}
	app := setupIAMRequestApp(svc)

	req := httptest.NewRequest("POST", "/", strings.NewReader("Action=CreateUser&UserName=alice"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 409, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	xmlStr := string(body)
	assert.Contains(t, xmlStr, "EntityAlreadyExists")
	assert.Contains(t, xmlStr, "<ErrorResponse>")
}

func TestIAMRequest_ValidationError(t *testing.T) {
	// CreateUser with missing UserName should return MissingParameter
	app := setupIAMRequestApp(&flexMockIAMService{})

	req := httptest.NewRequest("POST", "/", strings.NewReader("Action=CreateUser"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 400, resp.StatusCode)

	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "MissingParameter")
}
