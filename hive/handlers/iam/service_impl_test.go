package handlers_iam

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Interface compliance check
var _ IAMService = (*IAMServiceImpl)(nil)

func setupTestIAMService(t *testing.T) *IAMServiceImpl {
	t.Helper()
	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })

	nc, err := nats.Connect(ns.ClientURL())
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close() })

	masterKey, err := GenerateMasterKey()
	require.NoError(t, err)

	svc, err := NewIAMServiceImpl(nc, masterKey)
	require.NoError(t, err)
	return svc
}

func createTestUser(t *testing.T, svc *IAMServiceImpl, userName string) *iam.User {
	t.Helper()
	out, err := svc.CreateUser(&iam.CreateUserInput{
		UserName: aws.String(userName),
	})
	require.NoError(t, err)
	return out.User
}

// ============================================================================
// User CRUD Tests
// ============================================================================

func TestCreateUser(t *testing.T) {
	svc := setupTestIAMService(t)

	out, err := svc.CreateUser(&iam.CreateUserInput{
		UserName: aws.String("testuser"),
		Path:     aws.String("/developers/"),
		Tags: []*iam.Tag{
			{Key: aws.String("team"), Value: aws.String("backend")},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, out.User)
	assert.Equal(t, "testuser", *out.User.UserName)
	assert.Contains(t, *out.User.Arn, "testuser")
	assert.Equal(t, "/developers/", *out.User.Path)
	assert.True(t, len(*out.User.UserId) > 4)
	assert.Equal(t, "AIDA", (*out.User.UserId)[:4])
}

func TestCreateUser_DefaultPath(t *testing.T) {
	svc := setupTestIAMService(t)

	out, err := svc.CreateUser(&iam.CreateUserInput{
		UserName: aws.String("defaultpath"),
	})
	require.NoError(t, err)
	assert.Equal(t, "/", *out.User.Path)
}

func TestCreateUser_MissingUserName(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.CreateUser(&iam.CreateUserInput{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMInvalidInput)
}

func TestCreateUser_Duplicate(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.CreateUser(&iam.CreateUserInput{
		UserName: aws.String("duplicateuser"),
	})
	require.NoError(t, err)

	_, err = svc.CreateUser(&iam.CreateUserInput{
		UserName: aws.String("duplicateuser"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMEntityAlreadyExists)
}

func TestGetUser(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "getuser")

	out, err := svc.GetUser(&iam.GetUserInput{
		UserName: aws.String("getuser"),
	})
	require.NoError(t, err)
	assert.Equal(t, "getuser", *out.User.UserName)
}

func TestGetUser_NotFound(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.GetUser(&iam.GetUserInput{
		UserName: aws.String("nonexistent"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestListUsers(t *testing.T) {
	svc := setupTestIAMService(t)

	createTestUser(t, svc, "user1")
	createTestUser(t, svc, "user2")
	createTestUser(t, svc, "user3")

	out, err := svc.ListUsers(&iam.ListUsersInput{})
	require.NoError(t, err)
	assert.Len(t, out.Users, 3)

	names := make(map[string]bool)
	for _, u := range out.Users {
		names[*u.UserName] = true
	}
	assert.True(t, names["user1"])
	assert.True(t, names["user2"])
	assert.True(t, names["user3"])
}

func TestListUsers_Empty(t *testing.T) {
	svc := setupTestIAMService(t)

	out, err := svc.ListUsers(&iam.ListUsersInput{})
	require.NoError(t, err)
	assert.Len(t, out.Users, 0)
}

func TestListUsers_PathFilter(t *testing.T) {
	svc := setupTestIAMService(t)

	svc.CreateUser(&iam.CreateUserInput{
		UserName: aws.String("dev1"),
		Path:     aws.String("/developers/"),
	})
	svc.CreateUser(&iam.CreateUserInput{
		UserName: aws.String("admin1"),
		Path:     aws.String("/admins/"),
	})

	out, err := svc.ListUsers(&iam.ListUsersInput{
		PathPrefix: aws.String("/developers/"),
	})
	require.NoError(t, err)
	assert.Len(t, out.Users, 1)
	assert.Equal(t, "dev1", *out.Users[0].UserName)
}

func TestDeleteUser(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "deleteuser")

	_, err := svc.DeleteUser(&iam.DeleteUserInput{
		UserName: aws.String("deleteuser"),
	})
	require.NoError(t, err)

	_, err = svc.GetUser(&iam.GetUserInput{
		UserName: aws.String("deleteuser"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestDeleteUser_NotFound(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.DeleteUser(&iam.DeleteUserInput{
		UserName: aws.String("nonexistent"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestDeleteUser_WithAccessKeys(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "userWithKeys")

	_, err := svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("userWithKeys"),
	})
	require.NoError(t, err)

	_, err = svc.DeleteUser(&iam.DeleteUserInput{
		UserName: aws.String("userWithKeys"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMDeleteConflict)
}

// ============================================================================
// Access Key Tests
// ============================================================================

func TestCreateAccessKey(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "keyuser")

	out, err := svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("keyuser"),
	})
	require.NoError(t, err)
	require.NotNil(t, out.AccessKey)

	assert.Equal(t, "keyuser", *out.AccessKey.UserName)
	assert.Equal(t, "Active", *out.AccessKey.Status)
	assert.True(t, len(*out.AccessKey.AccessKeyId) >= 20)
	assert.Equal(t, "AKIA", (*out.AccessKey.AccessKeyId)[:4])
	assert.True(t, len(*out.AccessKey.SecretAccessKey) >= 30)
}

func TestCreateAccessKey_SecretIsDecryptable(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "decryptuser")

	out, err := svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("decryptuser"),
	})
	require.NoError(t, err)

	plaintextSecret := *out.AccessKey.SecretAccessKey
	accessKeyID := *out.AccessKey.AccessKeyId

	// Look up the stored key and verify the encrypted secret can be decrypted
	ak, err := svc.LookupAccessKey(accessKeyID)
	require.NoError(t, err)

	decrypted, err := DecryptSecret(ak.SecretAccessKey, svc.masterKey)
	require.NoError(t, err)
	assert.Equal(t, plaintextSecret, decrypted)
}

func TestCreateAccessKey_UserNotFound(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("nonexistent"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestCreateAccessKey_MaxLimit(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "limituser")

	_, err := svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("limituser"),
	})
	require.NoError(t, err)

	_, err = svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("limituser"),
	})
	require.NoError(t, err)

	// Third key should fail (AWS limit is 2)
	_, err = svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("limituser"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMLimitExceeded)
}

func TestListAccessKeys(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "listkeysuser")

	key1, _ := svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("listkeysuser"),
	})
	key2, _ := svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("listkeysuser"),
	})

	out, err := svc.ListAccessKeys(&iam.ListAccessKeysInput{
		UserName: aws.String("listkeysuser"),
	})
	require.NoError(t, err)
	assert.Len(t, out.AccessKeyMetadata, 2)

	keyIDs := make(map[string]bool)
	for _, k := range out.AccessKeyMetadata {
		keyIDs[*k.AccessKeyId] = true
	}
	assert.True(t, keyIDs[*key1.AccessKey.AccessKeyId])
	assert.True(t, keyIDs[*key2.AccessKey.AccessKeyId])
}

func TestDeleteAccessKey(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "delkeyuser")

	keyOut, err := svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("delkeyuser"),
	})
	require.NoError(t, err)
	keyID := *keyOut.AccessKey.AccessKeyId

	_, err = svc.DeleteAccessKey(&iam.DeleteAccessKeyInput{
		UserName:    aws.String("delkeyuser"),
		AccessKeyId: aws.String(keyID),
	})
	require.NoError(t, err)

	listOut, err := svc.ListAccessKeys(&iam.ListAccessKeysInput{
		UserName: aws.String("delkeyuser"),
	})
	require.NoError(t, err)
	assert.Len(t, listOut.AccessKeyMetadata, 0)

	// User should now be deletable (no access keys)
	_, err = svc.DeleteUser(&iam.DeleteUserInput{
		UserName: aws.String("delkeyuser"),
	})
	require.NoError(t, err)
}

func TestDeleteAccessKey_NotFound(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "delnotfounduser")

	_, err := svc.DeleteAccessKey(&iam.DeleteAccessKeyInput{
		UserName:    aws.String("delnotfounduser"),
		AccessKeyId: aws.String("AKIANONEXISTENT12345"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestUpdateAccessKey(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "updatekeyuser")

	keyOut, _ := svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("updatekeyuser"),
	})
	keyID := *keyOut.AccessKey.AccessKeyId

	// Deactivate
	_, err := svc.UpdateAccessKey(&iam.UpdateAccessKeyInput{
		AccessKeyId: aws.String(keyID),
		Status:      aws.String("Inactive"),
	})
	require.NoError(t, err)

	// Verify status changed
	listOut, _ := svc.ListAccessKeys(&iam.ListAccessKeysInput{
		UserName: aws.String("updatekeyuser"),
	})
	assert.Equal(t, "Inactive", *listOut.AccessKeyMetadata[0].Status)

	// Reactivate
	_, err = svc.UpdateAccessKey(&iam.UpdateAccessKeyInput{
		AccessKeyId: aws.String(keyID),
		Status:      aws.String("Active"),
	})
	require.NoError(t, err)

	listOut, _ = svc.ListAccessKeys(&iam.ListAccessKeysInput{
		UserName: aws.String("updatekeyuser"),
	})
	assert.Equal(t, "Active", *listOut.AccessKeyMetadata[0].Status)
}

func TestUpdateAccessKey_InvalidStatus(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "invalidstatususer")

	keyOut, _ := svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("invalidstatususer"),
	})

	_, err := svc.UpdateAccessKey(&iam.UpdateAccessKeyInput{
		AccessKeyId: keyOut.AccessKey.AccessKeyId,
		Status:      aws.String("Invalid"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMInvalidInput)
}

func TestUpdateAccessKey_NotFound(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.UpdateAccessKey(&iam.UpdateAccessKeyInput{
		AccessKeyId: aws.String("AKIANONEXISTENT12345"),
		Status:      aws.String("Inactive"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

// ============================================================================
// Auth Tests (LookupAccessKey)
// ============================================================================

func TestLookupAccessKey(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "lookupuser")

	keyOut, err := svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("lookupuser"),
	})
	require.NoError(t, err)

	ak, err := svc.LookupAccessKey(*keyOut.AccessKey.AccessKeyId)
	require.NoError(t, err)
	assert.Equal(t, "lookupuser", ak.UserName)
	assert.Equal(t, "Active", ak.Status)
	assert.NotEmpty(t, ak.SecretAccessKey) // encrypted secret
}

func TestLookupAccessKey_NotFound(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.LookupAccessKey("AKIANONEXISTENT12345")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestLookupAccessKey_InactiveKey(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "inactiveuser")

	keyOut, _ := svc.CreateAccessKey(&iam.CreateAccessKeyInput{
		UserName: aws.String("inactiveuser"),
	})
	keyID := *keyOut.AccessKey.AccessKeyId

	svc.UpdateAccessKey(&iam.UpdateAccessKeyInput{
		AccessKeyId: aws.String(keyID),
		Status:      aws.String("Inactive"),
	})

	// LookupAccessKey should still return the key — status check is the caller's job
	ak, err := svc.LookupAccessKey(keyID)
	require.NoError(t, err)
	assert.Equal(t, "Inactive", ak.Status)
}

// ============================================================================
// SeedRootUser Tests
// ============================================================================

func TestSeedRootUser(t *testing.T) {
	svc := setupTestIAMService(t)

	encryptedSecret, err := EncryptSecret("test-secret-key", svc.masterKey)
	require.NoError(t, err)

	err = svc.SeedRootUser(&BootstrapData{
		AccessKeyID:     "AKIAEXAMPLE123456789",
		EncryptedSecret: encryptedSecret,
		AccountID:       "000000000000",
	})
	require.NoError(t, err)

	// Verify root user exists
	out, err := svc.GetUser(&iam.GetUserInput{
		UserName: aws.String("root"),
	})
	require.NoError(t, err)
	assert.Equal(t, "root", *out.User.UserName)
	assert.Contains(t, *out.User.Arn, "root")

	// Verify access key exists
	ak, err := svc.LookupAccessKey("AKIAEXAMPLE123456789")
	require.NoError(t, err)
	assert.Equal(t, "root", ak.UserName)
	assert.Equal(t, "Active", ak.Status)

	// Verify secret is decryptable
	decrypted, err := DecryptSecret(ak.SecretAccessKey, svc.masterKey)
	require.NoError(t, err)
	assert.Equal(t, "test-secret-key", decrypted)
}

func TestSeedRootUser_Idempotent(t *testing.T) {
	svc := setupTestIAMService(t)

	encryptedSecret, _ := EncryptSecret("test-secret", svc.masterKey)
	data := &BootstrapData{
		AccessKeyID:     "AKIAEXAMPLE123456789",
		EncryptedSecret: encryptedSecret,
		AccountID:       "000000000000",
	}

	// First call seeds
	err := svc.SeedRootUser(data)
	require.NoError(t, err)

	// Second call should succeed (no-op, idempotent)
	err = svc.SeedRootUser(data)
	require.NoError(t, err)

	// Root user should still exist with original data
	out, err := svc.GetUser(&iam.GetUserInput{
		UserName: aws.String("root"),
	})
	require.NoError(t, err)
	assert.Equal(t, "root", *out.User.UserName)
}

// ============================================================================
// IsEmpty Tests
// ============================================================================

func TestIsEmpty_True(t *testing.T) {
	svc := setupTestIAMService(t)

	empty, err := svc.IsEmpty()
	require.NoError(t, err)
	assert.True(t, empty)
}

func TestIsEmpty_False(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "notempty")

	empty, err := svc.IsEmpty()
	require.NoError(t, err)
	assert.False(t, empty)
}

// ============================================================================
// Helper Function Tests
// ============================================================================

func TestGenerateUserID(t *testing.T) {
	id := generateUserID()
	assert.Equal(t, "AIDA", id[:4])
	assert.True(t, len(id) == 21) // AIDA + 17 hex chars

	// Two IDs should differ
	id2 := generateUserID()
	assert.NotEqual(t, id, id2)
}

func TestGenerateAccessKeyID(t *testing.T) {
	id := generateAccessKeyID()
	assert.Equal(t, "AKIA", id[:4])
	assert.True(t, len(id) == 24) // AKIA + 20 hex chars

	id2 := generateAccessKeyID()
	assert.NotEqual(t, id, id2)
}

func TestGenerateSecretAccessKey(t *testing.T) {
	secret := generateSecretAccessKey()
	assert.Len(t, secret, 40)

	secret2 := generateSecretAccessKey()
	assert.NotEqual(t, secret, secret2)
}
