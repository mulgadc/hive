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

const testAccountID = GlobalAccountID

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
	out, err := svc.CreateUser(testAccountID, &iam.CreateUserInput{
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

	out, err := svc.CreateUser(testAccountID, &iam.CreateUserInput{
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

	out, err := svc.CreateUser(testAccountID, &iam.CreateUserInput{
		UserName: aws.String("defaultpath"),
	})
	require.NoError(t, err)
	assert.Equal(t, "/", *out.User.Path)
}

func TestCreateUser_MissingUserName(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.CreateUser(testAccountID, &iam.CreateUserInput{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMInvalidInput)
}

func TestCreateUser_Duplicate(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.CreateUser(testAccountID, &iam.CreateUserInput{
		UserName: aws.String("duplicateuser"),
	})
	require.NoError(t, err)

	_, err = svc.CreateUser(testAccountID, &iam.CreateUserInput{
		UserName: aws.String("duplicateuser"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMEntityAlreadyExists)
}

func TestGetUser(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "getuser")

	out, err := svc.GetUser(testAccountID, &iam.GetUserInput{
		UserName: aws.String("getuser"),
	})
	require.NoError(t, err)
	assert.Equal(t, "getuser", *out.User.UserName)
}

func TestGetUser_NotFound(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.GetUser(testAccountID, &iam.GetUserInput{
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

	out, err := svc.ListUsers(testAccountID, &iam.ListUsersInput{})
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

	out, err := svc.ListUsers(testAccountID, &iam.ListUsersInput{})
	require.NoError(t, err)
	assert.Len(t, out.Users, 0)
}

func TestListUsers_PathFilter(t *testing.T) {
	svc := setupTestIAMService(t)

	svc.CreateUser(testAccountID, &iam.CreateUserInput{
		UserName: aws.String("dev1"),
		Path:     aws.String("/developers/"),
	})
	svc.CreateUser(testAccountID, &iam.CreateUserInput{
		UserName: aws.String("admin1"),
		Path:     aws.String("/admins/"),
	})

	out, err := svc.ListUsers(testAccountID, &iam.ListUsersInput{
		PathPrefix: aws.String("/developers/"),
	})
	require.NoError(t, err)
	assert.Len(t, out.Users, 1)
	assert.Equal(t, "dev1", *out.Users[0].UserName)
}

func TestDeleteUser(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "deleteuser")

	_, err := svc.DeleteUser(testAccountID, &iam.DeleteUserInput{
		UserName: aws.String("deleteuser"),
	})
	require.NoError(t, err)

	_, err = svc.GetUser(testAccountID, &iam.GetUserInput{
		UserName: aws.String("deleteuser"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestDeleteUser_NotFound(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.DeleteUser(testAccountID, &iam.DeleteUserInput{
		UserName: aws.String("nonexistent"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestDeleteUser_WithAccessKeys(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "userWithKeys")

	_, err := svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
		UserName: aws.String("userWithKeys"),
	})
	require.NoError(t, err)

	_, err = svc.DeleteUser(testAccountID, &iam.DeleteUserInput{
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

	out, err := svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
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

	out, err := svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
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

	_, err := svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
		UserName: aws.String("nonexistent"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestCreateAccessKey_MaxLimit(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "limituser")

	_, err := svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
		UserName: aws.String("limituser"),
	})
	require.NoError(t, err)

	_, err = svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
		UserName: aws.String("limituser"),
	})
	require.NoError(t, err)

	// Third key should fail (AWS limit is 2)
	_, err = svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
		UserName: aws.String("limituser"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMLimitExceeded)
}

func TestListAccessKeys(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "listkeysuser")

	key1, _ := svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
		UserName: aws.String("listkeysuser"),
	})
	key2, _ := svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
		UserName: aws.String("listkeysuser"),
	})

	out, err := svc.ListAccessKeys(testAccountID, &iam.ListAccessKeysInput{
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

	keyOut, err := svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
		UserName: aws.String("delkeyuser"),
	})
	require.NoError(t, err)
	keyID := *keyOut.AccessKey.AccessKeyId

	_, err = svc.DeleteAccessKey(testAccountID, &iam.DeleteAccessKeyInput{
		UserName:    aws.String("delkeyuser"),
		AccessKeyId: aws.String(keyID),
	})
	require.NoError(t, err)

	listOut, err := svc.ListAccessKeys(testAccountID, &iam.ListAccessKeysInput{
		UserName: aws.String("delkeyuser"),
	})
	require.NoError(t, err)
	assert.Len(t, listOut.AccessKeyMetadata, 0)

	// User should now be deletable (no access keys)
	_, err = svc.DeleteUser(testAccountID, &iam.DeleteUserInput{
		UserName: aws.String("delkeyuser"),
	})
	require.NoError(t, err)
}

func TestDeleteAccessKey_NotFound(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "delnotfounduser")

	_, err := svc.DeleteAccessKey(testAccountID, &iam.DeleteAccessKeyInput{
		UserName:    aws.String("delnotfounduser"),
		AccessKeyId: aws.String("AKIANONEXISTENT12345"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestUpdateAccessKey(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "updatekeyuser")

	keyOut, _ := svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
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
	listOut, _ := svc.ListAccessKeys(testAccountID, &iam.ListAccessKeysInput{
		UserName: aws.String("updatekeyuser"),
	})
	assert.Equal(t, "Inactive", *listOut.AccessKeyMetadata[0].Status)

	// Reactivate
	_, err = svc.UpdateAccessKey(&iam.UpdateAccessKeyInput{
		AccessKeyId: aws.String(keyID),
		Status:      aws.String("Active"),
	})
	require.NoError(t, err)

	listOut, _ = svc.ListAccessKeys(testAccountID, &iam.ListAccessKeysInput{
		UserName: aws.String("updatekeyuser"),
	})
	assert.Equal(t, "Active", *listOut.AccessKeyMetadata[0].Status)
}

func TestUpdateAccessKey_InvalidStatus(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "invalidstatususer")

	keyOut, _ := svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
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

	keyOut, err := svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
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

	keyOut, _ := svc.CreateAccessKey(testAccountID, &iam.CreateAccessKeyInput{
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
		AccountID:       GlobalAccountID,
	})
	require.NoError(t, err)

	// Verify root user exists at account-scoped key
	out, err := svc.GetUser(GlobalAccountID, &iam.GetUserInput{
		UserName: aws.String("root"),
	})
	require.NoError(t, err)
	assert.Equal(t, "root", *out.User.UserName)
	assert.Contains(t, *out.User.Arn, GlobalAccountID)
	assert.Contains(t, *out.User.Arn, "root")

	// Verify access key exists with AccountID
	ak, err := svc.LookupAccessKey("AKIAEXAMPLE123456789")
	require.NoError(t, err)
	assert.Equal(t, "root", ak.UserName)
	assert.Equal(t, GlobalAccountID, ak.AccountID)
	assert.Equal(t, "Active", ak.Status)

	// Verify secret is decryptable
	decrypted, err := DecryptSecret(ak.SecretAccessKey, svc.masterKey)
	require.NoError(t, err)
	assert.Equal(t, "test-secret-key", decrypted)

	// Verify global account record was created
	account, err := svc.GetAccount(GlobalAccountID)
	require.NoError(t, err)
	assert.Equal(t, GlobalAccountID, account.AccountID)
	assert.Equal(t, "Global", account.AccountName)
	assert.Equal(t, "ACTIVE", account.Status)
}

func TestSeedRootUser_Idempotent(t *testing.T) {
	svc := setupTestIAMService(t)

	encryptedSecret, _ := EncryptSecret("test-secret", svc.masterKey)
	data := &BootstrapData{
		AccessKeyID:     "AKIAEXAMPLE123456789",
		EncryptedSecret: encryptedSecret,
		AccountID:       GlobalAccountID,
	}

	// First call seeds
	err := svc.SeedRootUser(data)
	require.NoError(t, err)

	// Second call should succeed (no-op, idempotent)
	err = svc.SeedRootUser(data)
	require.NoError(t, err)

	// Root user should still exist with original data
	out, err := svc.GetUser(GlobalAccountID, &iam.GetUserInput{
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

// ============================================================================
// Policy CRUD Tests
// ============================================================================

// validPolicyDocument returns a valid IAM policy document JSON string.
func validPolicyDocument() string {
	return `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"ec2:DescribeInstances","Resource":"*"}]}`
}

func createTestPolicy(t *testing.T, svc *IAMServiceImpl, name string) *iam.Policy {
	t.Helper()
	out, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String(name),
		PolicyDocument: aws.String(validPolicyDocument()),
	})
	require.NoError(t, err)
	return out.Policy
}

func TestCreatePolicy(t *testing.T) {
	svc := setupTestIAMService(t)

	out, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("AllowEC2"),
		PolicyDocument: aws.String(validPolicyDocument()),
		Path:           aws.String("/devteam/"),
		Description:    aws.String("Allow EC2 describe"),
	})
	require.NoError(t, err)
	require.NotNil(t, out.Policy)
	assert.Equal(t, "AllowEC2", *out.Policy.PolicyName)
	assert.Equal(t, "/devteam/", *out.Policy.Path)
	assert.Equal(t, "Allow EC2 describe", *out.Policy.Description)
	assert.Equal(t, "v1", *out.Policy.DefaultVersionId)
	assert.Contains(t, *out.Policy.Arn, "policy/devteam/AllowEC2")
	assert.True(t, len(*out.Policy.PolicyId) > 4)
	assert.Equal(t, "ANPA", (*out.Policy.PolicyId)[:4])
	assert.Equal(t, int64(0), *out.Policy.AttachmentCount)
	assert.True(t, *out.Policy.IsAttachable)
}

func TestCreatePolicy_DefaultPath(t *testing.T) {
	svc := setupTestIAMService(t)

	out, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("DefaultPath"),
		PolicyDocument: aws.String(validPolicyDocument()),
	})
	require.NoError(t, err)
	assert.Equal(t, "/", *out.Policy.Path)
	assert.Contains(t, *out.Policy.Arn, "policy/DefaultPath")
}

func TestCreatePolicy_MissingName(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyDocument: aws.String(validPolicyDocument()),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMInvalidInput)
}

func TestCreatePolicy_MissingDocument(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName: aws.String("NoDoc"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMInvalidInput)
}

func TestCreatePolicy_InvalidJSON(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("BadJSON"),
		PolicyDocument: aws.String(`{not valid json`),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMMalformedPolicyDocument)
}

func TestCreatePolicy_InvalidVersion(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("BadVersion"),
		PolicyDocument: aws.String(`{"Version":"2008-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMMalformedPolicyDocument)
}

func TestCreatePolicy_NoStatements(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("NoStmts"),
		PolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":[]}`),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMMalformedPolicyDocument)
}

func TestCreatePolicy_InvalidEffect(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("BadEffect"),
		PolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Maybe","Action":"*","Resource":"*"}]}`),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMMalformedPolicyDocument)
}

func TestCreatePolicy_MissingAction(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("NoAction"),
		PolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Resource":"*"}]}`),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMMalformedPolicyDocument)
}

func TestCreatePolicy_MissingResource(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("NoResource"),
		PolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*"}]}`),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMMalformedPolicyDocument)
}

func TestCreatePolicy_Duplicate(t *testing.T) {
	svc := setupTestIAMService(t)

	createTestPolicy(t, svc, "DupPolicy")

	_, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("DupPolicy"),
		PolicyDocument: aws.String(validPolicyDocument()),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMEntityAlreadyExists)
}

func TestCreatePolicy_ArrayActions(t *testing.T) {
	svc := setupTestIAMService(t)

	doc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["ec2:DescribeInstances","ec2:RunInstances"],"Resource":"*"}]}`
	out, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("ArrayActions"),
		PolicyDocument: aws.String(doc),
	})
	require.NoError(t, err)
	assert.Equal(t, "ArrayActions", *out.Policy.PolicyName)
}

func TestGetPolicy(t *testing.T) {
	svc := setupTestIAMService(t)
	created := createTestPolicy(t, svc, "GetMe")

	out, err := svc.GetPolicy(testAccountID, &iam.GetPolicyInput{
		PolicyArn: created.Arn,
	})
	require.NoError(t, err)
	assert.Equal(t, "GetMe", *out.Policy.PolicyName)
	assert.Equal(t, *created.PolicyId, *out.Policy.PolicyId)
	assert.Equal(t, *created.Arn, *out.Policy.Arn)
	assert.Equal(t, "v1", *out.Policy.DefaultVersionId)
	assert.Equal(t, int64(0), *out.Policy.AttachmentCount)
}

func TestGetPolicy_NotFound(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.GetPolicy(testAccountID, &iam.GetPolicyInput{
		PolicyArn: aws.String("arn:aws:iam::000000000000:policy/Nonexistent"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestGetPolicy_MalformedARN(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.GetPolicy(testAccountID, &iam.GetPolicyInput{
		PolicyArn: aws.String("not-an-arn"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestGetPolicy_WithAttachments(t *testing.T) {
	svc := setupTestIAMService(t)
	created := createTestPolicy(t, svc, "AttachedPolicy")
	createTestUser(t, svc, "attachuser1")
	createTestUser(t, svc, "attachuser2")

	svc.AttachUserPolicy(testAccountID, &iam.AttachUserPolicyInput{
		UserName:  aws.String("attachuser1"),
		PolicyArn: created.Arn,
	})
	svc.AttachUserPolicy(testAccountID, &iam.AttachUserPolicyInput{
		UserName:  aws.String("attachuser2"),
		PolicyArn: created.Arn,
	})

	out, err := svc.GetPolicy(testAccountID, &iam.GetPolicyInput{
		PolicyArn: created.Arn,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), *out.Policy.AttachmentCount)
}

func TestGetPolicyVersion(t *testing.T) {
	svc := setupTestIAMService(t)
	created := createTestPolicy(t, svc, "VersionPolicy")

	out, err := svc.GetPolicyVersion(testAccountID, &iam.GetPolicyVersionInput{
		PolicyArn: created.Arn,
		VersionId: aws.String("v1"),
	})
	require.NoError(t, err)
	assert.Equal(t, "v1", *out.PolicyVersion.VersionId)
	assert.True(t, *out.PolicyVersion.IsDefaultVersion)
	assert.NotEmpty(t, *out.PolicyVersion.Document)

	// Verify the returned document is valid JSON
	doc, err := ValidatePolicyDocument(*out.PolicyVersion.Document)
	require.NoError(t, err)
	assert.Equal(t, "2012-10-17", doc.Version)
	assert.Len(t, doc.Statement, 1)
}

func TestGetPolicyVersion_InvalidVersion(t *testing.T) {
	svc := setupTestIAMService(t)
	created := createTestPolicy(t, svc, "VersionPolicy2")

	_, err := svc.GetPolicyVersion(testAccountID, &iam.GetPolicyVersionInput{
		PolicyArn: created.Arn,
		VersionId: aws.String("v2"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestGetPolicyVersion_PolicyNotFound(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.GetPolicyVersion(testAccountID, &iam.GetPolicyVersionInput{
		PolicyArn: aws.String("arn:aws:iam::000000000000:policy/Ghost"),
		VersionId: aws.String("v1"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestListPolicies(t *testing.T) {
	svc := setupTestIAMService(t)

	createTestPolicy(t, svc, "Policy1")
	createTestPolicy(t, svc, "Policy2")
	createTestPolicy(t, svc, "Policy3")

	out, err := svc.ListPolicies(testAccountID, &iam.ListPoliciesInput{})
	require.NoError(t, err)
	assert.Len(t, out.Policies, 3)

	names := make(map[string]bool)
	for _, p := range out.Policies {
		names[*p.PolicyName] = true
	}
	assert.True(t, names["Policy1"])
	assert.True(t, names["Policy2"])
	assert.True(t, names["Policy3"])
}

func TestListPolicies_Empty(t *testing.T) {
	svc := setupTestIAMService(t)

	out, err := svc.ListPolicies(testAccountID, &iam.ListPoliciesInput{})
	require.NoError(t, err)
	assert.Len(t, out.Policies, 0)
}

func TestDeletePolicy(t *testing.T) {
	svc := setupTestIAMService(t)
	created := createTestPolicy(t, svc, "DeleteMe")

	_, err := svc.DeletePolicy(testAccountID, &iam.DeletePolicyInput{
		PolicyArn: created.Arn,
	})
	require.NoError(t, err)

	// Confirm it's gone
	_, err = svc.GetPolicy(testAccountID, &iam.GetPolicyInput{
		PolicyArn: created.Arn,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestDeletePolicy_NotFound(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.DeletePolicy(testAccountID, &iam.DeletePolicyInput{
		PolicyArn: aws.String("arn:aws:iam::000000000000:policy/Ghost"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestDeletePolicy_AttachedConflict(t *testing.T) {
	svc := setupTestIAMService(t)
	created := createTestPolicy(t, svc, "Attached")
	createTestUser(t, svc, "conflictuser")

	_, err := svc.AttachUserPolicy(testAccountID, &iam.AttachUserPolicyInput{
		UserName:  aws.String("conflictuser"),
		PolicyArn: created.Arn,
	})
	require.NoError(t, err)

	_, err = svc.DeletePolicy(testAccountID, &iam.DeletePolicyInput{
		PolicyArn: created.Arn,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMDeleteConflict)

	// Detach, then delete should succeed
	_, err = svc.DetachUserPolicy(testAccountID, &iam.DetachUserPolicyInput{
		UserName:  aws.String("conflictuser"),
		PolicyArn: created.Arn,
	})
	require.NoError(t, err)

	_, err = svc.DeletePolicy(testAccountID, &iam.DeletePolicyInput{
		PolicyArn: created.Arn,
	})
	require.NoError(t, err)
}

// ============================================================================
// Policy Attachment Tests
// ============================================================================

func TestAttachUserPolicy(t *testing.T) {
	svc := setupTestIAMService(t)
	created := createTestPolicy(t, svc, "AttachPolicy")
	createTestUser(t, svc, "attachme")

	_, err := svc.AttachUserPolicy(testAccountID, &iam.AttachUserPolicyInput{
		UserName:  aws.String("attachme"),
		PolicyArn: created.Arn,
	})
	require.NoError(t, err)

	// Verify via list
	out, err := svc.ListAttachedUserPolicies(testAccountID, &iam.ListAttachedUserPoliciesInput{
		UserName: aws.String("attachme"),
	})
	require.NoError(t, err)
	require.Len(t, out.AttachedPolicies, 1)
	assert.Equal(t, "AttachPolicy", *out.AttachedPolicies[0].PolicyName)
	assert.Equal(t, *created.Arn, *out.AttachedPolicies[0].PolicyArn)
}

func TestAttachUserPolicy_Idempotent(t *testing.T) {
	svc := setupTestIAMService(t)
	created := createTestPolicy(t, svc, "IdempotentPolicy")
	createTestUser(t, svc, "idempotentuser")

	input := &iam.AttachUserPolicyInput{
		UserName:  aws.String("idempotentuser"),
		PolicyArn: created.Arn,
	}

	_, err := svc.AttachUserPolicy(testAccountID, input)
	require.NoError(t, err)

	// Attach same policy again — should succeed silently
	_, err = svc.AttachUserPolicy(testAccountID, input)
	require.NoError(t, err)

	// Should still have exactly 1 attachment
	out, err := svc.ListAttachedUserPolicies(testAccountID, &iam.ListAttachedUserPoliciesInput{
		UserName: aws.String("idempotentuser"),
	})
	require.NoError(t, err)
	assert.Len(t, out.AttachedPolicies, 1)
}

func TestAttachUserPolicy_NonexistentPolicy(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "orphanuser")

	_, err := svc.AttachUserPolicy(testAccountID, &iam.AttachUserPolicyInput{
		UserName:  aws.String("orphanuser"),
		PolicyArn: aws.String("arn:aws:iam::000000000000:policy/Ghost"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestAttachUserPolicy_NonexistentUser(t *testing.T) {
	svc := setupTestIAMService(t)
	created := createTestPolicy(t, svc, "OrphanPolicy")

	_, err := svc.AttachUserPolicy(testAccountID, &iam.AttachUserPolicyInput{
		UserName:  aws.String("ghostuser"),
		PolicyArn: created.Arn,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestDetachUserPolicy(t *testing.T) {
	svc := setupTestIAMService(t)
	created := createTestPolicy(t, svc, "DetachPolicy")
	createTestUser(t, svc, "detachme")

	_, err := svc.AttachUserPolicy(testAccountID, &iam.AttachUserPolicyInput{
		UserName:  aws.String("detachme"),
		PolicyArn: created.Arn,
	})
	require.NoError(t, err)

	_, err = svc.DetachUserPolicy(testAccountID, &iam.DetachUserPolicyInput{
		UserName:  aws.String("detachme"),
		PolicyArn: created.Arn,
	})
	require.NoError(t, err)

	// Verify detached
	out, err := svc.ListAttachedUserPolicies(testAccountID, &iam.ListAttachedUserPoliciesInput{
		UserName: aws.String("detachme"),
	})
	require.NoError(t, err)
	assert.Len(t, out.AttachedPolicies, 0)
}

func TestDetachUserPolicy_NotAttached(t *testing.T) {
	svc := setupTestIAMService(t)
	created := createTestPolicy(t, svc, "NeverAttached")
	createTestUser(t, svc, "cleanuser")

	_, err := svc.DetachUserPolicy(testAccountID, &iam.DetachUserPolicyInput{
		UserName:  aws.String("cleanuser"),
		PolicyArn: created.Arn,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestDetachUserPolicy_NonexistentUser(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.DetachUserPolicy(testAccountID, &iam.DetachUserPolicyInput{
		UserName:  aws.String("ghostuser"),
		PolicyArn: aws.String("arn:aws:iam::000000000000:policy/Whatever"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

func TestListAttachedUserPolicies(t *testing.T) {
	svc := setupTestIAMService(t)

	p1 := createTestPolicy(t, svc, "ListPolicy1")
	p2 := createTestPolicy(t, svc, "ListPolicy2")
	createTestUser(t, svc, "listpuser")

	svc.AttachUserPolicy(testAccountID, &iam.AttachUserPolicyInput{
		UserName:  aws.String("listpuser"),
		PolicyArn: p1.Arn,
	})
	svc.AttachUserPolicy(testAccountID, &iam.AttachUserPolicyInput{
		UserName:  aws.String("listpuser"),
		PolicyArn: p2.Arn,
	})

	out, err := svc.ListAttachedUserPolicies(testAccountID, &iam.ListAttachedUserPoliciesInput{
		UserName: aws.String("listpuser"),
	})
	require.NoError(t, err)
	assert.Len(t, out.AttachedPolicies, 2)

	names := make(map[string]bool)
	for _, p := range out.AttachedPolicies {
		names[*p.PolicyName] = true
	}
	assert.True(t, names["ListPolicy1"])
	assert.True(t, names["ListPolicy2"])
}

func TestListAttachedUserPolicies_Empty(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "nopolicies")

	out, err := svc.ListAttachedUserPolicies(testAccountID, &iam.ListAttachedUserPoliciesInput{
		UserName: aws.String("nopolicies"),
	})
	require.NoError(t, err)
	assert.Len(t, out.AttachedPolicies, 0)
}

func TestListAttachedUserPolicies_NonexistentUser(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.ListAttachedUserPolicies(testAccountID, &iam.ListAttachedUserPoliciesInput{
		UserName: aws.String("ghostuser"),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

// ============================================================================
// GetUserPolicies (internal) Tests
// ============================================================================

func TestGetUserPolicies(t *testing.T) {
	svc := setupTestIAMService(t)

	doc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"ec2:DescribeInstances","Resource":"*"}]}`
	p1, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("InternalPolicy1"),
		PolicyDocument: aws.String(doc),
	})
	require.NoError(t, err)

	doc2 := `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"ec2:TerminateInstances","Resource":"*"}]}`
	p2, err := svc.CreatePolicy(testAccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("InternalPolicy2"),
		PolicyDocument: aws.String(doc2),
	})
	require.NoError(t, err)

	createTestUser(t, svc, "evaluser")

	svc.AttachUserPolicy(testAccountID, &iam.AttachUserPolicyInput{
		UserName:  aws.String("evaluser"),
		PolicyArn: p1.Policy.Arn,
	})
	svc.AttachUserPolicy(testAccountID, &iam.AttachUserPolicyInput{
		UserName:  aws.String("evaluser"),
		PolicyArn: p2.Policy.Arn,
	})

	docs, err := svc.GetUserPolicies(testAccountID, "evaluser")
	require.NoError(t, err)
	assert.Len(t, docs, 2)
	assert.Equal(t, "2012-10-17", docs[0].Version)
	assert.Equal(t, "2012-10-17", docs[1].Version)
}

func TestGetUserPolicies_NoPolicies(t *testing.T) {
	svc := setupTestIAMService(t)
	createTestUser(t, svc, "emptyuser")

	docs, err := svc.GetUserPolicies(testAccountID, "emptyuser")
	require.NoError(t, err)
	assert.Len(t, docs, 0)
}

func TestGetUserPolicies_NonexistentUser(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.GetUserPolicies(testAccountID, "ghostuser")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), awserrors.ErrorIAMNoSuchEntity)
}

// ============================================================================
// ValidatePolicyDocument Tests
// ============================================================================

func TestValidatePolicyDocument_Valid(t *testing.T) {
	doc, err := ValidatePolicyDocument(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`)
	require.NoError(t, err)
	assert.Equal(t, "2012-10-17", doc.Version)
	assert.Len(t, doc.Statement, 1)
	assert.Equal(t, "Allow", doc.Statement[0].Effect)
}

func TestValidatePolicyDocument_MultipleStatements(t *testing.T) {
	doc, err := ValidatePolicyDocument(`{
		"Version":"2012-10-17",
		"Statement":[
			{"Sid":"AllowEC2","Effect":"Allow","Action":["ec2:DescribeInstances","ec2:RunInstances"],"Resource":"*"},
			{"Effect":"Deny","Action":"ec2:TerminateInstances","Resource":"*"}
		]
	}`)
	require.NoError(t, err)
	assert.Len(t, doc.Statement, 2)
	assert.Equal(t, "AllowEC2", doc.Statement[0].Sid)
	assert.Len(t, doc.Statement[0].Action, 2)
}

func TestValidatePolicyDocument_BadJSON(t *testing.T) {
	_, err := ValidatePolicyDocument(`{broken`)
	assert.Error(t, err)
}

func TestValidatePolicyDocument_WrongVersion(t *testing.T) {
	_, err := ValidatePolicyDocument(`{"Version":"2008-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported policy version")
}

func TestValidatePolicyDocument_EmptyStatements(t *testing.T) {
	_, err := ValidatePolicyDocument(`{"Version":"2012-10-17","Statement":[]}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one statement")
}

func TestValidatePolicyDocument_BadEffect(t *testing.T) {
	_, err := ValidatePolicyDocument(`{"Version":"2012-10-17","Statement":[{"Effect":"Maybe","Action":"*","Resource":"*"}]}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Effect must be Allow or Deny")
}

func TestValidatePolicyDocument_MissingAction(t *testing.T) {
	_, err := ValidatePolicyDocument(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Resource":"*"}]}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Action is required")
}

func TestValidatePolicyDocument_MissingResource(t *testing.T) {
	_, err := ValidatePolicyDocument(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*"}]}`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Resource is required")
}

// ============================================================================
// Helper Function Tests (Policy)
// ============================================================================

func TestGeneratePolicyID(t *testing.T) {
	id := generatePolicyID()
	assert.Equal(t, "ANPA", id[:4])
	assert.Len(t, id, 21) // ANPA + 17 hex chars

	id2 := generatePolicyID()
	assert.NotEqual(t, id, id2)
}
