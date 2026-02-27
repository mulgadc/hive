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
	createUserFn      func(*iam.CreateUserInput) (*iam.CreateUserOutput, error)
	getUserFn         func(*iam.GetUserInput) (*iam.GetUserOutput, error)
	listUsersFn       func(*iam.ListUsersInput) (*iam.ListUsersOutput, error)
	deleteUserFn      func(*iam.DeleteUserInput) (*iam.DeleteUserOutput, error)
	createAccessKeyFn func(*iam.CreateAccessKeyInput) (*iam.CreateAccessKeyOutput, error)
	listAccessKeysFn  func(*iam.ListAccessKeysInput) (*iam.ListAccessKeysOutput, error)
	deleteAccessKeyFn func(*iam.DeleteAccessKeyInput) (*iam.DeleteAccessKeyOutput, error)
	updateAccessKeyFn func(*iam.UpdateAccessKeyInput) (*iam.UpdateAccessKeyOutput, error)
}

func (m *flexMockIAMService) CreateUser(input *iam.CreateUserInput) (*iam.CreateUserOutput, error) {
	if m.createUserFn != nil {
		return m.createUserFn(input)
	}
	return &iam.CreateUserOutput{}, nil
}

func (m *flexMockIAMService) GetUser(input *iam.GetUserInput) (*iam.GetUserOutput, error) {
	if m.getUserFn != nil {
		return m.getUserFn(input)
	}
	return &iam.GetUserOutput{}, nil
}

func (m *flexMockIAMService) ListUsers(input *iam.ListUsersInput) (*iam.ListUsersOutput, error) {
	if m.listUsersFn != nil {
		return m.listUsersFn(input)
	}
	return &iam.ListUsersOutput{}, nil
}

func (m *flexMockIAMService) DeleteUser(input *iam.DeleteUserInput) (*iam.DeleteUserOutput, error) {
	if m.deleteUserFn != nil {
		return m.deleteUserFn(input)
	}
	return &iam.DeleteUserOutput{}, nil
}

func (m *flexMockIAMService) CreateAccessKey(input *iam.CreateAccessKeyInput) (*iam.CreateAccessKeyOutput, error) {
	if m.createAccessKeyFn != nil {
		return m.createAccessKeyFn(input)
	}
	return &iam.CreateAccessKeyOutput{}, nil
}

func (m *flexMockIAMService) ListAccessKeys(input *iam.ListAccessKeysInput) (*iam.ListAccessKeysOutput, error) {
	if m.listAccessKeysFn != nil {
		return m.listAccessKeysFn(input)
	}
	return &iam.ListAccessKeysOutput{}, nil
}

func (m *flexMockIAMService) DeleteAccessKey(input *iam.DeleteAccessKeyInput) (*iam.DeleteAccessKeyOutput, error) {
	if m.deleteAccessKeyFn != nil {
		return m.deleteAccessKeyFn(input)
	}
	return &iam.DeleteAccessKeyOutput{}, nil
}

func (m *flexMockIAMService) UpdateAccessKey(input *iam.UpdateAccessKeyInput) (*iam.UpdateAccessKeyOutput, error) {
	if m.updateAccessKeyFn != nil {
		return m.updateAccessKeyFn(input)
	}
	return &iam.UpdateAccessKeyOutput{}, nil
}

func (m *flexMockIAMService) CreatePolicy(_ *iam.CreatePolicyInput) (*iam.CreatePolicyOutput, error) {
	return &iam.CreatePolicyOutput{}, nil
}

func (m *flexMockIAMService) GetPolicy(_ *iam.GetPolicyInput) (*iam.GetPolicyOutput, error) {
	return &iam.GetPolicyOutput{}, nil
}

func (m *flexMockIAMService) GetPolicyVersion(_ *iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error) {
	return &iam.GetPolicyVersionOutput{}, nil
}

func (m *flexMockIAMService) ListPolicies(_ *iam.ListPoliciesInput) (*iam.ListPoliciesOutput, error) {
	return &iam.ListPoliciesOutput{}, nil
}

func (m *flexMockIAMService) DeletePolicy(_ *iam.DeletePolicyInput) (*iam.DeletePolicyOutput, error) {
	return &iam.DeletePolicyOutput{}, nil
}

func (m *flexMockIAMService) AttachUserPolicy(_ *iam.AttachUserPolicyInput) (*iam.AttachUserPolicyOutput, error) {
	return &iam.AttachUserPolicyOutput{}, nil
}

func (m *flexMockIAMService) DetachUserPolicy(_ *iam.DetachUserPolicyInput) (*iam.DetachUserPolicyOutput, error) {
	return &iam.DetachUserPolicyOutput{}, nil
}

func (m *flexMockIAMService) ListAttachedUserPolicies(_ *iam.ListAttachedUserPoliciesInput) (*iam.ListAttachedUserPoliciesOutput, error) {
	return &iam.ListAttachedUserPoliciesOutput{}, nil
}

func (m *flexMockIAMService) GetUserPolicies(_ string) ([]handlers_iam.PolicyDocument, error) {
	return nil, nil
}

func (m *flexMockIAMService) LookupAccessKey(_ string) (*handlers_iam.AccessKey, error) {
	return nil, errors.New("not implemented")
}

func (m *flexMockIAMService) SeedRootUser(_ *handlers_iam.BootstrapData) error { return nil }
func (m *flexMockIAMService) IsEmpty() (bool, error)                           { return true, nil }

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
		return gw.IAM_Request(c)
	})
	return app
}

func TestIAMRequest_CreateUser_Success(t *testing.T) {
	svc := &flexMockIAMService{
		createUserFn: func(input *iam.CreateUserInput) (*iam.CreateUserOutput, error) {
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
		listUsersFn: func(_ *iam.ListUsersInput) (*iam.ListUsersOutput, error) {
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
		createUserFn: func(_ *iam.CreateUserInput) (*iam.CreateUserOutput, error) {
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
