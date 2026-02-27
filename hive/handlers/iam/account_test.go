package handlers_iam

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Account CRUD Tests
// ============================================================================

func TestCreateAccount(t *testing.T) {
	svc := setupTestIAMService(t)

	acc1, err := svc.CreateAccount("Team Alpha")
	require.NoError(t, err)
	assert.Equal(t, "000000000001", acc1.AccountID)
	assert.Equal(t, "Team Alpha", acc1.AccountName)
	assert.Equal(t, "ACTIVE", acc1.Status)
	assert.NotEmpty(t, acc1.CreatedAt)

	// Second account gets sequential ID
	acc2, err := svc.CreateAccount("Team Beta")
	require.NoError(t, err)
	assert.Equal(t, "000000000002", acc2.AccountID)
	assert.Equal(t, "Team Beta", acc2.AccountName)
}

func TestCreateAccount_SequentialIDs(t *testing.T) {
	svc := setupTestIAMService(t)

	var prevID string
	for i := 0; i < 5; i++ {
		acc, err := svc.CreateAccount("account")
		require.NoError(t, err)
		assert.Len(t, acc.AccountID, 12)
		if prevID != "" {
			assert.Greater(t, acc.AccountID, prevID)
		}
		prevID = acc.AccountID
	}
}

func TestGetAccount(t *testing.T) {
	svc := setupTestIAMService(t)

	created, err := svc.CreateAccount("Lookup Test")
	require.NoError(t, err)

	got, err := svc.GetAccount(created.AccountID)
	require.NoError(t, err)
	assert.Equal(t, created.AccountID, got.AccountID)
	assert.Equal(t, "Lookup Test", got.AccountName)
	assert.Equal(t, "ACTIVE", got.Status)
}

func TestGetAccount_NotFound(t *testing.T) {
	svc := setupTestIAMService(t)

	_, err := svc.GetAccount("999999999999")
	assert.Error(t, err)
}

func TestListAccounts(t *testing.T) {
	svc := setupTestIAMService(t)

	svc.CreateAccount("Acct1")
	svc.CreateAccount("Acct2")
	svc.CreateAccount("Acct3")

	accounts, err := svc.ListAccounts()
	require.NoError(t, err)
	assert.Len(t, accounts, 3)

	names := make(map[string]bool)
	for _, a := range accounts {
		names[a.AccountName] = true
	}
	assert.True(t, names["Acct1"])
	assert.True(t, names["Acct2"])
	assert.True(t, names["Acct3"])
}

func TestListAccounts_Empty(t *testing.T) {
	svc := setupTestIAMService(t)

	accounts, err := svc.ListAccounts()
	require.NoError(t, err)
	assert.Len(t, accounts, 0)
}

// ============================================================================
// Account-Scoped User Tests
// ============================================================================

func TestAccountScopedUsers(t *testing.T) {
	svc := setupTestIAMService(t)

	acc1, err := svc.CreateAccount("Org A")
	require.NoError(t, err)
	acc2, err := svc.CreateAccount("Org B")
	require.NoError(t, err)

	// Create same username in both accounts
	_, err = svc.CreateUser(acc1.AccountID, &iam.CreateUserInput{
		UserName: aws.String("admin"),
	})
	require.NoError(t, err)

	_, err = svc.CreateUser(acc2.AccountID, &iam.CreateUserInput{
		UserName: aws.String("admin"),
	})
	require.NoError(t, err)

	// Both should be independently retrievable
	out1, err := svc.GetUser(acc1.AccountID, &iam.GetUserInput{
		UserName: aws.String("admin"),
	})
	require.NoError(t, err)
	assert.Contains(t, *out1.User.Arn, acc1.AccountID)

	out2, err := svc.GetUser(acc2.AccountID, &iam.GetUserInput{
		UserName: aws.String("admin"),
	})
	require.NoError(t, err)
	assert.Contains(t, *out2.User.Arn, acc2.AccountID)

	// Listing users in acc1 should only return acc1's user
	list1, err := svc.ListUsers(acc1.AccountID, &iam.ListUsersInput{})
	require.NoError(t, err)
	assert.Len(t, list1.Users, 1)

	list2, err := svc.ListUsers(acc2.AccountID, &iam.ListUsersInput{})
	require.NoError(t, err)
	assert.Len(t, list2.Users, 1)

	// Deleting in one account shouldn't affect the other
	_, err = svc.DeleteUser(acc1.AccountID, &iam.DeleteUserInput{
		UserName: aws.String("admin"),
	})
	require.NoError(t, err)

	_, err = svc.GetUser(acc2.AccountID, &iam.GetUserInput{
		UserName: aws.String("admin"),
	})
	require.NoError(t, err) // acc2's admin still exists
}

// ============================================================================
// Account-Scoped Policy Tests
// ============================================================================

func TestAccountScopedPolicies(t *testing.T) {
	svc := setupTestIAMService(t)

	acc1, err := svc.CreateAccount("Policy Org A")
	require.NoError(t, err)
	acc2, err := svc.CreateAccount("Policy Org B")
	require.NoError(t, err)

	doc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`

	// Create same policy name in both accounts
	p1, err := svc.CreatePolicy(acc1.AccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("AdminAccess"),
		PolicyDocument: aws.String(doc),
	})
	require.NoError(t, err)
	assert.Contains(t, *p1.Policy.Arn, acc1.AccountID)

	p2, err := svc.CreatePolicy(acc2.AccountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("AdminAccess"),
		PolicyDocument: aws.String(doc),
	})
	require.NoError(t, err)
	assert.Contains(t, *p2.Policy.Arn, acc2.AccountID)

	// Listing in each account should be independent
	list1, err := svc.ListPolicies(acc1.AccountID, &iam.ListPoliciesInput{})
	require.NoError(t, err)
	assert.Len(t, list1.Policies, 1)

	list2, err := svc.ListPolicies(acc2.AccountID, &iam.ListPoliciesInput{})
	require.NoError(t, err)
	assert.Len(t, list2.Policies, 1)
}

// ============================================================================
// Access Key Account ID Tests
// ============================================================================

func TestAccessKeyReturnsAccountID(t *testing.T) {
	svc := setupTestIAMService(t)

	acc, err := svc.CreateAccount("Key Org")
	require.NoError(t, err)

	_, err = svc.CreateUser(acc.AccountID, &iam.CreateUserInput{
		UserName: aws.String("keyuser"),
	})
	require.NoError(t, err)

	akOut, err := svc.CreateAccessKey(acc.AccountID, &iam.CreateAccessKeyInput{
		UserName: aws.String("keyuser"),
	})
	require.NoError(t, err)

	// LookupAccessKey should return the correct account ID
	ak, err := svc.LookupAccessKey(*akOut.AccessKey.AccessKeyId)
	require.NoError(t, err)
	assert.Equal(t, acc.AccountID, ak.AccountID)
	assert.Equal(t, "keyuser", ak.UserName)
}

// ============================================================================
// SeedRootUser Account-Scoped Tests
// ============================================================================

func TestSeedRootUser_AccountScoped(t *testing.T) {
	svc := setupTestIAMService(t)

	encryptedSecret, err := EncryptSecret("root-secret", svc.masterKey)
	require.NoError(t, err)

	err = svc.SeedRootUser(&BootstrapData{
		AccessKeyID:     "AKIAROOTEXAMPLE12345",
		EncryptedSecret: encryptedSecret,
		AccountID:       GlobalAccountID,
	})
	require.NoError(t, err)

	// Root user stored at 000000000000:root
	out, err := svc.GetUser(GlobalAccountID, &iam.GetUserInput{
		UserName: aws.String("root"),
	})
	require.NoError(t, err)
	assert.Equal(t, "root", *out.User.UserName)
	assert.Contains(t, *out.User.Arn, GlobalAccountID)

	// Access key has correct AccountID
	ak, err := svc.LookupAccessKey("AKIAROOTEXAMPLE12345")
	require.NoError(t, err)
	assert.Equal(t, GlobalAccountID, ak.AccountID)
	assert.Equal(t, "root", ak.UserName)

	// Global account record was created
	account, err := svc.GetAccount(GlobalAccountID)
	require.NoError(t, err)
	assert.Equal(t, GlobalAccountID, account.AccountID)
	assert.Equal(t, "Global", account.AccountName)
	assert.Equal(t, "ACTIVE", account.Status)
}
