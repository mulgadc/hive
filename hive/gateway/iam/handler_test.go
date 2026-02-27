package gateway_iam

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/mulgadc/hive/hive/awserrors"
	handlers_iam "github.com/mulgadc/hive/hive/handlers/iam"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// stubIAMService returns empty non-nil outputs for all methods.
type stubIAMService struct{}

func (s *stubIAMService) CreateUser(_ *iam.CreateUserInput) (*iam.CreateUserOutput, error) {
	return &iam.CreateUserOutput{}, nil
}

func (s *stubIAMService) GetUser(_ *iam.GetUserInput) (*iam.GetUserOutput, error) {
	return &iam.GetUserOutput{}, nil
}

func (s *stubIAMService) ListUsers(_ *iam.ListUsersInput) (*iam.ListUsersOutput, error) {
	return &iam.ListUsersOutput{}, nil
}

func (s *stubIAMService) DeleteUser(_ *iam.DeleteUserInput) (*iam.DeleteUserOutput, error) {
	return &iam.DeleteUserOutput{}, nil
}

func (s *stubIAMService) CreateAccessKey(_ *iam.CreateAccessKeyInput) (*iam.CreateAccessKeyOutput, error) {
	return &iam.CreateAccessKeyOutput{}, nil
}

func (s *stubIAMService) ListAccessKeys(_ *iam.ListAccessKeysInput) (*iam.ListAccessKeysOutput, error) {
	return &iam.ListAccessKeysOutput{}, nil
}

func (s *stubIAMService) DeleteAccessKey(_ *iam.DeleteAccessKeyInput) (*iam.DeleteAccessKeyOutput, error) {
	return &iam.DeleteAccessKeyOutput{}, nil
}

func (s *stubIAMService) UpdateAccessKey(_ *iam.UpdateAccessKeyInput) (*iam.UpdateAccessKeyOutput, error) {
	return &iam.UpdateAccessKeyOutput{}, nil
}

func (s *stubIAMService) LookupAccessKey(_ string) (*handlers_iam.AccessKey, error) {
	return nil, nil
}

func (s *stubIAMService) SeedRootUser(_ *handlers_iam.BootstrapData) error { return nil }
func (s *stubIAMService) IsEmpty() (bool, error)                           { return true, nil }

func TestCreateUser(t *testing.T) {
	svc := &stubIAMService{}
	tests := []struct {
		name    string
		input   *iam.CreateUserInput
		wantErr string
	}{
		{"nil UserName", &iam.CreateUserInput{}, awserrors.ErrorMissingParameter},
		{"empty UserName", &iam.CreateUserInput{UserName: aws.String("")}, awserrors.ErrorMissingParameter},
		{"valid", &iam.CreateUserInput{UserName: aws.String("alice")}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CreateUser(tc.input, svc)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tc.wantErr, err.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGetUser(t *testing.T) {
	svc := &stubIAMService{}
	tests := []struct {
		name    string
		input   *iam.GetUserInput
		wantErr string
	}{
		{"nil UserName", &iam.GetUserInput{}, awserrors.ErrorMissingParameter},
		{"empty UserName", &iam.GetUserInput{UserName: aws.String("")}, awserrors.ErrorMissingParameter},
		{"valid", &iam.GetUserInput{UserName: aws.String("alice")}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GetUser(tc.input, svc)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tc.wantErr, err.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestListUsers(t *testing.T) {
	svc := &stubIAMService{}
	_, err := ListUsers(&iam.ListUsersInput{}, svc)
	require.NoError(t, err)
}

func TestDeleteUser(t *testing.T) {
	svc := &stubIAMService{}
	tests := []struct {
		name    string
		input   *iam.DeleteUserInput
		wantErr string
	}{
		{"nil UserName", &iam.DeleteUserInput{}, awserrors.ErrorMissingParameter},
		{"empty UserName", &iam.DeleteUserInput{UserName: aws.String("")}, awserrors.ErrorMissingParameter},
		{"valid", &iam.DeleteUserInput{UserName: aws.String("alice")}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DeleteUser(tc.input, svc)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tc.wantErr, err.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCreateAccessKey(t *testing.T) {
	svc := &stubIAMService{}
	tests := []struct {
		name    string
		input   *iam.CreateAccessKeyInput
		wantErr string
	}{
		{"nil UserName", &iam.CreateAccessKeyInput{}, awserrors.ErrorMissingParameter},
		{"empty UserName", &iam.CreateAccessKeyInput{UserName: aws.String("")}, awserrors.ErrorMissingParameter},
		{"valid", &iam.CreateAccessKeyInput{UserName: aws.String("alice")}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CreateAccessKey(tc.input, svc)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tc.wantErr, err.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestListAccessKeys(t *testing.T) {
	svc := &stubIAMService{}
	tests := []struct {
		name    string
		input   *iam.ListAccessKeysInput
		wantErr string
	}{
		{"nil UserName", &iam.ListAccessKeysInput{}, awserrors.ErrorMissingParameter},
		{"empty UserName", &iam.ListAccessKeysInput{UserName: aws.String("")}, awserrors.ErrorMissingParameter},
		{"valid", &iam.ListAccessKeysInput{UserName: aws.String("alice")}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ListAccessKeys(tc.input, svc)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tc.wantErr, err.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDeleteAccessKey(t *testing.T) {
	svc := &stubIAMService{}
	tests := []struct {
		name    string
		input   *iam.DeleteAccessKeyInput
		wantErr string
	}{
		{"nil UserName", &iam.DeleteAccessKeyInput{AccessKeyId: aws.String("AKIA123")}, awserrors.ErrorMissingParameter},
		{"empty UserName", &iam.DeleteAccessKeyInput{UserName: aws.String(""), AccessKeyId: aws.String("AKIA123")}, awserrors.ErrorMissingParameter},
		{"nil AccessKeyId", &iam.DeleteAccessKeyInput{UserName: aws.String("alice")}, awserrors.ErrorMissingParameter},
		{"empty AccessKeyId", &iam.DeleteAccessKeyInput{UserName: aws.String("alice"), AccessKeyId: aws.String("")}, awserrors.ErrorMissingParameter},
		{"valid", &iam.DeleteAccessKeyInput{UserName: aws.String("alice"), AccessKeyId: aws.String("AKIA123")}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DeleteAccessKey(tc.input, svc)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tc.wantErr, err.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestUpdateAccessKey(t *testing.T) {
	svc := &stubIAMService{}
	tests := []struct {
		name    string
		input   *iam.UpdateAccessKeyInput
		wantErr string
	}{
		{"nil AccessKeyId", &iam.UpdateAccessKeyInput{Status: aws.String("Active")}, awserrors.ErrorMissingParameter},
		{"empty AccessKeyId", &iam.UpdateAccessKeyInput{AccessKeyId: aws.String(""), Status: aws.String("Active")}, awserrors.ErrorMissingParameter},
		{"nil Status", &iam.UpdateAccessKeyInput{AccessKeyId: aws.String("AKIA123")}, awserrors.ErrorMissingParameter},
		{"empty Status", &iam.UpdateAccessKeyInput{AccessKeyId: aws.String("AKIA123"), Status: aws.String("")}, awserrors.ErrorMissingParameter},
		{"valid", &iam.UpdateAccessKeyInput{AccessKeyId: aws.String("AKIA123"), Status: aws.String("Active")}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UpdateAccessKey(tc.input, svc)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tc.wantErr, err.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}
