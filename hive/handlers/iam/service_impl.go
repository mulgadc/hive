package handlers_iam

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/nats-io/nats.go"
)

const (
	KVBucketUsers          = "hive-iam-users"
	KVBucketAccessKeys     = "hive-iam-access-keys"
	KVBucketPolicies       = "hive-iam-policies"
	KVBucketAccounts       = "hive-accounts"
	KVBucketAccountCounter = "hive-account-counter"

	GlobalAccountID = "000000000000"

	maxAccessKeysPerUser = 2
)

// IAMServiceImpl implements IAM operations using NATS JetStream KV.
type IAMServiceImpl struct {
	js                   nats.JetStreamContext
	usersBucket          nats.KeyValue
	accessKeysBucket     nats.KeyValue
	policiesBucket       nats.KeyValue
	accountsBucket       nats.KeyValue
	accountCounterBucket nats.KeyValue
	masterKey            []byte
}

// NewIAMServiceImpl creates a new IAM service backed by NATS JetStream KV.
func NewIAMServiceImpl(natsConn *nats.Conn, masterKey []byte) (*IAMServiceImpl, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(masterKey))
	}

	js, err := natsConn.JetStream()
	if err != nil {
		return nil, fmt.Errorf("get JetStream context: %w", err)
	}

	usersBucket, err := getOrCreateBucket(js, KVBucketUsers, 10)
	if err != nil {
		return nil, fmt.Errorf("init users bucket: %w", err)
	}

	accessKeysBucket, err := getOrCreateBucket(js, KVBucketAccessKeys, 5)
	if err != nil {
		return nil, fmt.Errorf("init access keys bucket: %w", err)
	}

	policiesBucket, err := getOrCreateBucket(js, KVBucketPolicies, 10)
	if err != nil {
		return nil, fmt.Errorf("init policies bucket: %w", err)
	}

	accountsBucket, err := getOrCreateBucket(js, KVBucketAccounts, 5)
	if err != nil {
		return nil, fmt.Errorf("init accounts bucket: %w", err)
	}

	accountCounterBucket, err := getOrCreateBucket(js, KVBucketAccountCounter, 5)
	if err != nil {
		return nil, fmt.Errorf("init account counter bucket: %w", err)
	}

	slog.Info("IAM service initialized",
		"users_bucket", KVBucketUsers,
		"access_keys_bucket", KVBucketAccessKeys,
		"policies_bucket", KVBucketPolicies,
		"accounts_bucket", KVBucketAccounts)

	return &IAMServiceImpl{
		js:                   js,
		usersBucket:          usersBucket,
		accessKeysBucket:     accessKeysBucket,
		policiesBucket:       policiesBucket,
		accountsBucket:       accountsBucket,
		accountCounterBucket: accountCounterBucket,
		masterKey:            masterKey,
	}, nil
}

func getOrCreateBucket(js nats.JetStreamContext, name string, history uint8) (nats.KeyValue, error) {
	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  name,
		History: history,
	})
	if err != nil {
		kv, err = js.KeyValue(name)
		if err != nil {
			return nil, err
		}
	}
	return kv, nil
}

// ---------------------------------------------------------------------------
// User CRUD
// ---------------------------------------------------------------------------

func (s *IAMServiceImpl) CreateUser(accountID string, input *iam.CreateUserInput) (*iam.CreateUserOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	userName := *input.UserName
	path := "/"
	if input.Path != nil {
		path = *input.Path
	}

	kvKey := accountID + ":" + userName
	user := User{
		UserName:         userName,
		UserID:           generateUserID(),
		AccountID:        accountID,
		ARN:              fmt.Sprintf("arn:aws:iam::%s:user%s%s", accountID, path, userName),
		Path:             path,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
		AccessKeys:       []string{},
		Tags:             []Tag{},
		AttachedPolicies: []string{},
	}

	for _, tag := range input.Tags {
		if tag.Key != nil && tag.Value != nil {
			user.Tags = append(user.Tags, Tag{Key: *tag.Key, Value: *tag.Value})
		}
	}

	data, err := json.Marshal(user)
	if err != nil {
		return nil, fmt.Errorf("marshal user: %w", err)
	}

	// Atomic create — fails if key already exists (race-safe)
	if _, err := s.usersBucket.Create(kvKey, data); err != nil {
		if errors.Is(err, nats.ErrKeyExists) {
			return nil, errors.New(awserrors.ErrorIAMEntityAlreadyExists)
		}
		return nil, fmt.Errorf("store user: %w", err)
	}

	slog.Info("IAM user created", "accountID", accountID, "userName", userName, "userID", user.UserID)

	createdAt, err := time.Parse(time.RFC3339, user.CreatedAt)
	if err != nil {
		slog.Warn("Failed to parse user CreatedAt", "userName", userName, "createdAt", user.CreatedAt, "err", err)
	}
	return &iam.CreateUserOutput{
		User: &iam.User{
			UserName:   aws.String(user.UserName),
			UserId:     aws.String(user.UserID),
			Arn:        aws.String(user.ARN),
			Path:       aws.String(user.Path),
			CreateDate: aws.Time(createdAt),
		},
	}, nil
}

func (s *IAMServiceImpl) GetUser(accountID string, input *iam.GetUserInput) (*iam.GetUserOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	user, err := s.getUser(accountID, *input.UserName)
	if err != nil {
		return nil, err
	}

	createdAt, err := time.Parse(time.RFC3339, user.CreatedAt)
	if err != nil {
		slog.Warn("Failed to parse user CreatedAt", "userName", user.UserName, "createdAt", user.CreatedAt, "err", err)
	}
	return &iam.GetUserOutput{
		User: &iam.User{
			UserName:   aws.String(user.UserName),
			UserId:     aws.String(user.UserID),
			Arn:        aws.String(user.ARN),
			Path:       aws.String(user.Path),
			CreateDate: aws.Time(createdAt),
		},
	}, nil
}

func (s *IAMServiceImpl) ListUsers(accountID string, input *iam.ListUsersInput) (*iam.ListUsersOutput, error) {
	keys, err := s.usersBucket.Keys()
	if err != nil {
		if errors.Is(err, nats.ErrNoKeysFound) {
			return &iam.ListUsersOutput{
				Users:       []*iam.User{},
				IsTruncated: aws.Bool(false),
			}, nil
		}
		return nil, fmt.Errorf("list user keys: %w", err)
	}

	pathPrefix := "/"
	if input.PathPrefix != nil {
		pathPrefix = *input.PathPrefix
	}

	keyPrefix := accountID + ":"
	var users []*iam.User
	for _, key := range keys {
		if !strings.HasPrefix(key, keyPrefix) {
			continue
		}

		entry, err := s.usersBucket.Get(key)
		if err != nil {
			if errors.Is(err, nats.ErrKeyNotFound) {
				slog.Debug("ListUsers: user key disappeared (concurrent delete)", "key", key)
			} else {
				slog.Warn("ListUsers: failed to get user", "key", key, "err", err)
			}
			continue
		}

		var user User
		if err := json.Unmarshal(entry.Value(), &user); err != nil {
			slog.Warn("ListUsers: failed to unmarshal user", "key", key, "err", err)
			continue
		}

		if !strings.HasPrefix(user.Path, pathPrefix) {
			continue
		}

		createdAt, err := time.Parse(time.RFC3339, user.CreatedAt)
		if err != nil {
			slog.Warn("Failed to parse user CreatedAt", "userName", user.UserName, "createdAt", user.CreatedAt, "err", err)
		}
		users = append(users, &iam.User{
			UserName:   aws.String(user.UserName),
			UserId:     aws.String(user.UserID),
			Arn:        aws.String(user.ARN),
			Path:       aws.String(user.Path),
			CreateDate: aws.Time(createdAt),
		})
	}

	return &iam.ListUsersOutput{
		Users:       users,
		IsTruncated: aws.Bool(false),
	}, nil
}

func (s *IAMServiceImpl) DeleteUser(accountID string, input *iam.DeleteUserInput) (*iam.DeleteUserOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	userName := *input.UserName
	kvKey := accountID + ":" + userName

	user, err := s.getUser(accountID, userName)
	if err != nil {
		return nil, err
	}

	if len(user.AccessKeys) > 0 {
		return nil, errors.New(awserrors.ErrorIAMDeleteConflict)
	}

	if err := s.usersBucket.Delete(kvKey); err != nil {
		return nil, fmt.Errorf("delete user: %w", err)
	}

	slog.Info("IAM user deleted", "accountID", accountID, "userName", userName)
	return &iam.DeleteUserOutput{}, nil
}

// ---------------------------------------------------------------------------
// Access Key Lifecycle
// ---------------------------------------------------------------------------

func (s *IAMServiceImpl) CreateAccessKey(accountID string, input *iam.CreateAccessKeyInput) (*iam.CreateAccessKeyOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	userName := *input.UserName
	userKVKey := accountID + ":" + userName

	user, err := s.getUser(accountID, userName)
	if err != nil {
		return nil, err
	}

	if len(user.AccessKeys) >= maxAccessKeysPerUser {
		return nil, errors.New(awserrors.ErrorIAMLimitExceeded)
	}

	accessKeyID := generateAccessKeyID()
	secretAccessKey := generateSecretAccessKey()

	encryptedSecret, err := EncryptSecret(secretAccessKey, s.masterKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt secret: %w", err)
	}

	ak := AccessKey{
		AccessKeyID:     accessKeyID,
		SecretAccessKey: encryptedSecret,
		UserName:        userName,
		AccountID:       accountID,
		Status:          "Active",
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}

	akData, err := json.Marshal(ak)
	if err != nil {
		return nil, fmt.Errorf("marshal access key: %w", err)
	}

	if _, err := s.accessKeysBucket.Put(accessKeyID, akData); err != nil {
		return nil, fmt.Errorf("store access key: %w", err)
	}

	// Update user's access key list
	user.AccessKeys = append(user.AccessKeys, accessKeyID)
	userData, err := json.Marshal(user)
	if err != nil {
		if rbErr := s.accessKeysBucket.Delete(accessKeyID); rbErr != nil {
			slog.Error("Rollback failed: orphaned access key", "accessKeyID", accessKeyID, "err", rbErr)
		}
		return nil, fmt.Errorf("marshal user: %w", err)
	}

	if _, err := s.usersBucket.Put(userKVKey, userData); err != nil {
		if rbErr := s.accessKeysBucket.Delete(accessKeyID); rbErr != nil {
			slog.Error("Rollback failed: orphaned access key", "accessKeyID", accessKeyID, "err", rbErr)
		}
		return nil, fmt.Errorf("update user: %w", err)
	}

	slog.Info("IAM access key created", "accountID", accountID, "userName", userName, "accessKeyID", accessKeyID)

	createdAt, err := time.Parse(time.RFC3339, ak.CreatedAt)
	if err != nil {
		slog.Warn("Failed to parse access key CreatedAt", "accessKeyID", accessKeyID, "createdAt", ak.CreatedAt, "err", err)
	}
	return &iam.CreateAccessKeyOutput{
		AccessKey: &iam.AccessKey{
			AccessKeyId:     aws.String(accessKeyID),
			SecretAccessKey: aws.String(secretAccessKey), // plaintext — only time it's returned
			UserName:        aws.String(userName),
			Status:          aws.String("Active"),
			CreateDate:      aws.Time(createdAt),
		},
	}, nil
}

func (s *IAMServiceImpl) ListAccessKeys(accountID string, input *iam.ListAccessKeysInput) (*iam.ListAccessKeysOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	user, err := s.getUser(accountID, *input.UserName)
	if err != nil {
		return nil, err
	}

	var metadata []*iam.AccessKeyMetadata
	for _, keyID := range user.AccessKeys {
		entry, err := s.accessKeysBucket.Get(keyID)
		if err != nil {
			if errors.Is(err, nats.ErrKeyNotFound) {
				slog.Debug("ListAccessKeys: access key disappeared (concurrent delete)", "keyID", keyID)
			} else {
				slog.Warn("ListAccessKeys: failed to get access key", "keyID", keyID, "err", err)
			}
			continue
		}

		var ak AccessKey
		if err := json.Unmarshal(entry.Value(), &ak); err != nil {
			slog.Warn("ListAccessKeys: failed to unmarshal access key", "keyID", keyID, "err", err)
			continue
		}

		createdAt, err := time.Parse(time.RFC3339, ak.CreatedAt)
		if err != nil {
			slog.Warn("Failed to parse access key CreatedAt", "keyID", keyID, "createdAt", ak.CreatedAt, "err", err)
		}
		metadata = append(metadata, &iam.AccessKeyMetadata{
			AccessKeyId: aws.String(ak.AccessKeyID),
			UserName:    aws.String(ak.UserName),
			Status:      aws.String(ak.Status),
			CreateDate:  aws.Time(createdAt),
		})
	}

	return &iam.ListAccessKeysOutput{
		AccessKeyMetadata: metadata,
		IsTruncated:       aws.Bool(false),
	}, nil
}

func (s *IAMServiceImpl) DeleteAccessKey(accountID string, input *iam.DeleteAccessKeyInput) (*iam.DeleteAccessKeyOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}
	if input.AccessKeyId == nil || *input.AccessKeyId == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	userName := *input.UserName
	accessKeyID := *input.AccessKeyId
	userKVKey := accountID + ":" + userName

	user, err := s.getUser(accountID, userName)
	if err != nil {
		return nil, err
	}

	// Find and remove the access key reference from the user
	found := false
	remaining := make([]string, 0, len(user.AccessKeys))
	for _, keyID := range user.AccessKeys {
		if keyID == accessKeyID {
			found = true
		} else {
			remaining = append(remaining, keyID)
		}
	}

	if !found {
		return nil, errors.New(awserrors.ErrorIAMNoSuchEntity)
	}

	if err := s.accessKeysBucket.Delete(accessKeyID); err != nil {
		return nil, fmt.Errorf("delete access key: %w", err)
	}

	user.AccessKeys = remaining
	userData, err := json.Marshal(user)
	if err != nil {
		return nil, fmt.Errorf("marshal user: %w", err)
	}

	if _, err := s.usersBucket.Put(userKVKey, userData); err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}

	slog.Info("IAM access key deleted", "accountID", accountID, "userName", userName, "accessKeyID", accessKeyID)
	return &iam.DeleteAccessKeyOutput{}, nil
}

func (s *IAMServiceImpl) UpdateAccessKey(input *iam.UpdateAccessKeyInput) (*iam.UpdateAccessKeyOutput, error) {
	if input.AccessKeyId == nil || *input.AccessKeyId == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}
	if input.Status == nil || *input.Status == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	status := *input.Status
	if status != "Active" && status != "Inactive" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	accessKeyID := *input.AccessKeyId

	entry, err := s.accessKeysBucket.Get(accessKeyID)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, errors.New(awserrors.ErrorIAMNoSuchEntity)
		}
		return nil, fmt.Errorf("get access key: %w", err)
	}

	var ak AccessKey
	if err := json.Unmarshal(entry.Value(), &ak); err != nil {
		return nil, fmt.Errorf("unmarshal access key: %w", err)
	}

	ak.Status = status
	data, err := json.Marshal(ak)
	if err != nil {
		return nil, fmt.Errorf("marshal access key: %w", err)
	}

	if _, err := s.accessKeysBucket.Put(accessKeyID, data); err != nil {
		return nil, fmt.Errorf("update access key: %w", err)
	}

	slog.Info("IAM access key updated", "accessKeyID", accessKeyID, "status", status)
	return &iam.UpdateAccessKeyOutput{}, nil
}

// ---------------------------------------------------------------------------
// Auth (internal — used by SigV4 middleware and bootstrap)
// ---------------------------------------------------------------------------

// LookupAccessKey retrieves an access key by its ID. Returns the full record
// including the encrypted secret, for use by the SigV4 middleware.
func (s *IAMServiceImpl) LookupAccessKey(accessKeyID string) (*AccessKey, error) {
	entry, err := s.accessKeysBucket.Get(accessKeyID)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, errors.New(awserrors.ErrorIAMNoSuchEntity)
		}
		return nil, fmt.Errorf("lookup access key: %w", err)
	}

	var ak AccessKey
	if err := json.Unmarshal(entry.Value(), &ak); err != nil {
		return nil, fmt.Errorf("unmarshal access key: %w", err)
	}
	return &ak, nil
}

// SeedRootUser consumes bootstrap data to create the root IAM user and access
// key in NATS KV. Uses conditional create (put-if-not-exists) for multi-node
// race safety — the first node to call this wins; others skip silently.
// Also creates the global account record (000000000000) if it doesn't exist.
func (s *IAMServiceImpl) SeedRootUser(data *BootstrapData) error {
	// Ensure the global account record exists
	globalAccount := Account{
		AccountID:   GlobalAccountID,
		AccountName: "Global",
		Status:      "ACTIVE",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	accountData, err := json.Marshal(globalAccount)
	if err != nil {
		return fmt.Errorf("marshal global account: %w", err)
	}
	if _, err := s.accountsBucket.Create(GlobalAccountID, accountData); err != nil && !errors.Is(err, nats.ErrKeyExists) {
		return fmt.Errorf("seed global account: %w", err)
	}

	kvKey := GlobalAccountID + ":root"
	rootUser := User{
		UserName:         "root",
		UserID:           "AIDAAAAAAAAAAAAAAAAA",
		AccountID:        GlobalAccountID,
		ARN:              fmt.Sprintf("arn:aws:iam::%s:root", GlobalAccountID),
		Path:             "/",
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
		AccessKeys:       []string{data.AccessKeyID},
		Tags:             []Tag{},
		AttachedPolicies: []string{},
	}

	userData, err := json.Marshal(rootUser)
	if err != nil {
		return fmt.Errorf("marshal root user: %w", err)
	}

	// Conditional create — fails if key already exists (another node seeded first)
	_, err = s.usersBucket.Create(kvKey, userData)
	if errors.Is(err, nats.ErrKeyExists) {
		slog.Info("Root user already seeded by another node, skipping")
		return nil
	}
	if err != nil {
		return fmt.Errorf("seed root user: %w", err)
	}

	// Create access key entry (also conditional)
	ak := AccessKey{
		AccessKeyID:     data.AccessKeyID,
		SecretAccessKey: data.EncryptedSecret,
		UserName:        "root",
		AccountID:       GlobalAccountID,
		Status:          "Active",
		CreatedAt:       rootUser.CreatedAt,
	}

	akData, err := json.Marshal(ak)
	if err != nil {
		return fmt.Errorf("marshal root access key: %w", err)
	}

	_, err = s.accessKeysBucket.Create(data.AccessKeyID, akData)
	if errors.Is(err, nats.ErrKeyExists) {
		slog.Info("Root access key already seeded by another node, skipping")
		return nil
	}
	if err != nil {
		return fmt.Errorf("seed root access key: %w", err)
	}

	slog.Info("Root IAM user seeded", "accountID", GlobalAccountID, "accessKeyID", data.AccessKeyID)
	return nil
}

// IsEmpty returns true if the users bucket has no entries.
func (s *IAMServiceImpl) IsEmpty() (bool, error) {
	keys, err := s.usersBucket.Keys()
	if err != nil {
		if errors.Is(err, nats.ErrNoKeysFound) {
			return true, nil
		}
		return false, fmt.Errorf("check users bucket: %w", err)
	}
	return len(keys) == 0, nil
}

// ---------------------------------------------------------------------------
// Account Operations
// ---------------------------------------------------------------------------

// CreateAccount creates a new account with a sequentially assigned 12-digit ID.
// Uses a CAS loop on the counter bucket for safe concurrent ID assignment.
func (s *IAMServiceImpl) CreateAccount(name string) (*Account, error) {
	if name == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	var accountID string
	for {
		// Read current counter value
		entry, err := s.accountCounterBucket.Get("next_id")
		if errors.Is(err, nats.ErrKeyNotFound) {
			// First account ever — start at 1 (000000000000 is the global account)
			accountID = fmt.Sprintf("%012d", 1)
			if _, err := s.accountCounterBucket.Create("next_id", []byte("2")); err != nil {
				if errors.Is(err, nats.ErrKeyExists) {
					continue // Another node raced us, retry
				}
				return nil, fmt.Errorf("init account counter: %w", err)
			}
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read account counter: %w", err)
		}

		nextID, err := strconv.ParseInt(string(entry.Value()), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse account counter: %w", err)
		}
		accountID = fmt.Sprintf("%012d", nextID)

		// CAS update: increment counter, fail if revision changed
		newVal := []byte(strconv.FormatInt(nextID+1, 10))
		if _, err := s.accountCounterBucket.Update("next_id", newVal, entry.Revision()); err != nil {
			if errors.Is(err, nats.ErrKeyExists) {
				continue // CAS conflict, retry
			}
			return nil, fmt.Errorf("update account counter: %w", err)
		}
		break
	}

	account := Account{
		AccountID:   accountID,
		AccountName: name,
		Status:      "ACTIVE",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.Marshal(account)
	if err != nil {
		return nil, fmt.Errorf("marshal account: %w", err)
	}

	if _, err := s.accountsBucket.Create(accountID, data); err != nil {
		return nil, fmt.Errorf("store account: %w", err)
	}

	slog.Info("Account created", "accountID", accountID, "name", name)
	return &account, nil
}

// GetAccount retrieves an account by its 12-digit ID.
func (s *IAMServiceImpl) GetAccount(accountID string) (*Account, error) {
	entry, err := s.accountsBucket.Get(accountID)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, errors.New(awserrors.ErrorIAMNoSuchEntity)
		}
		return nil, fmt.Errorf("get account: %w", err)
	}

	var account Account
	if err := json.Unmarshal(entry.Value(), &account); err != nil {
		return nil, fmt.Errorf("unmarshal account: %w", err)
	}
	return &account, nil
}

// ListAccounts returns all accounts.
func (s *IAMServiceImpl) ListAccounts() ([]*Account, error) {
	keys, err := s.accountsBucket.Keys()
	if err != nil {
		if errors.Is(err, nats.ErrNoKeysFound) {
			return []*Account{}, nil
		}
		return nil, fmt.Errorf("list account keys: %w", err)
	}

	accounts := make([]*Account, 0, len(keys))
	for _, key := range keys {
		entry, err := s.accountsBucket.Get(key)
		if err != nil {
			if errors.Is(err, nats.ErrKeyNotFound) {
				continue
			}
			return nil, fmt.Errorf("get account %s: %w", key, err)
		}

		var account Account
		if err := json.Unmarshal(entry.Value(), &account); err != nil {
			slog.Warn("ListAccounts: failed to unmarshal account", "key", key, "err", err)
			continue
		}
		accounts = append(accounts, &account)
	}
	return accounts, nil
}

// ---------------------------------------------------------------------------
// Policy CRUD
// ---------------------------------------------------------------------------

func (s *IAMServiceImpl) CreatePolicy(accountID string, input *iam.CreatePolicyInput) (*iam.CreatePolicyOutput, error) {
	if input.PolicyName == nil || *input.PolicyName == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}
	if input.PolicyDocument == nil || *input.PolicyDocument == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	policyName := *input.PolicyName
	kvKey := accountID + ":" + policyName

	if _, err := ValidatePolicyDocument(*input.PolicyDocument); err != nil {
		slog.Debug("CreatePolicy: invalid policy document", "policyName", policyName, "err", err)
		return nil, errors.New(awserrors.ErrorIAMMalformedPolicyDocument)
	}

	path := aws.StringValue(input.Path)
	if path == "" {
		path = "/"
	}

	policy := Policy{
		PolicyName:     policyName,
		PolicyID:       generatePolicyID(),
		ARN:            fmt.Sprintf("arn:aws:iam::%s:policy%s%s", accountID, path, policyName),
		Path:           path,
		Description:    aws.StringValue(input.Description),
		PolicyDocument: *input.PolicyDocument,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		DefaultVersion: "v1",
		Tags:           []Tag{},
	}

	data, err := json.Marshal(policy)
	if err != nil {
		return nil, fmt.Errorf("marshal policy: %w", err)
	}

	if _, err := s.policiesBucket.Create(kvKey, data); err != nil {
		if errors.Is(err, nats.ErrKeyExists) {
			return nil, errors.New(awserrors.ErrorIAMEntityAlreadyExists)
		}
		return nil, fmt.Errorf("store policy: %w", err)
	}

	slog.Info("IAM policy created", "accountID", accountID, "policyName", policyName, "policyID", policy.PolicyID)

	createdAt, err := time.Parse(time.RFC3339, policy.CreatedAt)
	if err != nil {
		slog.Warn("Failed to parse policy CreatedAt", "policyName", policy.PolicyName, "createdAt", policy.CreatedAt, "err", err)
	}
	return &iam.CreatePolicyOutput{
		Policy: &iam.Policy{
			PolicyName:       aws.String(policy.PolicyName),
			PolicyId:         aws.String(policy.PolicyID),
			Arn:              aws.String(policy.ARN),
			Path:             aws.String(policy.Path),
			Description:      aws.String(policy.Description),
			DefaultVersionId: aws.String(policy.DefaultVersion),
			CreateDate:       aws.Time(createdAt),
			AttachmentCount:  aws.Int64(0),
			IsAttachable:     aws.Bool(true),
		},
	}, nil
}

func (s *IAMServiceImpl) GetPolicy(accountID string, input *iam.GetPolicyInput) (*iam.GetPolicyOutput, error) {
	if input.PolicyArn == nil || *input.PolicyArn == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	policy, err := s.getPolicyByARN(accountID, *input.PolicyArn)
	if err != nil {
		return nil, err
	}

	attachmentCount, err := s.countPolicyAttachments(accountID, policy.ARN)
	if err != nil {
		return nil, fmt.Errorf("check policy attachments: %w", err)
	}

	createdAt, err := time.Parse(time.RFC3339, policy.CreatedAt)
	if err != nil {
		slog.Warn("Failed to parse policy CreatedAt", "policyName", policy.PolicyName, "createdAt", policy.CreatedAt, "err", err)
	}
	return &iam.GetPolicyOutput{
		Policy: &iam.Policy{
			PolicyName:       aws.String(policy.PolicyName),
			PolicyId:         aws.String(policy.PolicyID),
			Arn:              aws.String(policy.ARN),
			Path:             aws.String(policy.Path),
			Description:      aws.String(policy.Description),
			DefaultVersionId: aws.String(policy.DefaultVersion),
			CreateDate:       aws.Time(createdAt),
			AttachmentCount:  aws.Int64(attachmentCount),
			IsAttachable:     aws.Bool(true),
		},
	}, nil
}

func (s *IAMServiceImpl) GetPolicyVersion(accountID string, input *iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error) {
	if input.PolicyArn == nil || *input.PolicyArn == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}
	if input.VersionId == nil || *input.VersionId == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	policy, err := s.getPolicyByARN(accountID, *input.PolicyArn)
	if err != nil {
		return nil, err
	}

	// We only support v1 — reject other version IDs
	if *input.VersionId != "v1" {
		return nil, errors.New(awserrors.ErrorIAMNoSuchEntity)
	}

	createdAt, err := time.Parse(time.RFC3339, policy.CreatedAt)
	if err != nil {
		slog.Warn("Failed to parse policy CreatedAt", "policyName", policy.PolicyName, "createdAt", policy.CreatedAt, "err", err)
	}
	return &iam.GetPolicyVersionOutput{
		PolicyVersion: &iam.PolicyVersion{
			Document:         aws.String(policy.PolicyDocument),
			VersionId:        aws.String("v1"),
			IsDefaultVersion: aws.Bool(true),
			CreateDate:       aws.Time(createdAt),
		},
	}, nil
}

func (s *IAMServiceImpl) ListPolicies(accountID string, input *iam.ListPoliciesInput) (*iam.ListPoliciesOutput, error) {
	keys, err := s.policiesBucket.Keys()
	if err != nil {
		if errors.Is(err, nats.ErrNoKeysFound) {
			return &iam.ListPoliciesOutput{
				Policies:    []*iam.Policy{},
				IsTruncated: aws.Bool(false),
			}, nil
		}
		return nil, fmt.Errorf("list policy keys: %w", err)
	}

	keyPrefix := accountID + ":"
	var policies []*iam.Policy
	for _, key := range keys {
		if !strings.HasPrefix(key, keyPrefix) {
			continue
		}

		entry, err := s.policiesBucket.Get(key)
		if err != nil {
			if errors.Is(err, nats.ErrKeyNotFound) {
				slog.Debug("ListPolicies: policy key disappeared (concurrent delete)", "key", key)
			} else {
				slog.Warn("ListPolicies: failed to get policy", "key", key, "err", err)
			}
			continue
		}

		var policy Policy
		if err := json.Unmarshal(entry.Value(), &policy); err != nil {
			slog.Warn("ListPolicies: failed to unmarshal policy", "key", key, "err", err)
			continue
		}

		createdAt, err := time.Parse(time.RFC3339, policy.CreatedAt)
		if err != nil {
			slog.Warn("Failed to parse policy CreatedAt", "policyName", policy.PolicyName, "createdAt", policy.CreatedAt, "err", err)
		}
		attachCount, err := s.countPolicyAttachments(accountID, policy.ARN)
		if err != nil {
			return nil, fmt.Errorf("count policy attachments: %w", err)
		}
		policies = append(policies, &iam.Policy{
			PolicyName:       aws.String(policy.PolicyName),
			PolicyId:         aws.String(policy.PolicyID),
			Arn:              aws.String(policy.ARN),
			Path:             aws.String(policy.Path),
			DefaultVersionId: aws.String(policy.DefaultVersion),
			CreateDate:       aws.Time(createdAt),
			AttachmentCount:  aws.Int64(attachCount),
			IsAttachable:     aws.Bool(true),
		})
	}

	return &iam.ListPoliciesOutput{
		Policies:    policies,
		IsTruncated: aws.Bool(false),
	}, nil
}

func (s *IAMServiceImpl) DeletePolicy(accountID string, input *iam.DeletePolicyInput) (*iam.DeletePolicyOutput, error) {
	if input.PolicyArn == nil || *input.PolicyArn == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	policy, err := s.getPolicyByARN(accountID, *input.PolicyArn)
	if err != nil {
		return nil, err
	}

	attachCount, err := s.countPolicyAttachments(accountID, policy.ARN)
	if err != nil {
		return nil, fmt.Errorf("check policy attachments: %w", err)
	}
	if attachCount > 0 {
		return nil, errors.New(awserrors.ErrorIAMDeleteConflict)
	}

	kvKey := accountID + ":" + policy.PolicyName
	if err := s.policiesBucket.Delete(kvKey); err != nil {
		return nil, fmt.Errorf("delete policy: %w", err)
	}

	slog.Info("IAM policy deleted", "accountID", accountID, "policyName", policy.PolicyName)
	return &iam.DeletePolicyOutput{}, nil
}

// ---------------------------------------------------------------------------
// Policy Attachment
// ---------------------------------------------------------------------------

func (s *IAMServiceImpl) AttachUserPolicy(accountID string, input *iam.AttachUserPolicyInput) (*iam.AttachUserPolicyOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}
	if input.PolicyArn == nil || *input.PolicyArn == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	userName := *input.UserName
	policyARN := *input.PolicyArn
	userKVKey := accountID + ":" + userName

	// Verify policy exists
	if _, err := s.getPolicyByARN(accountID, policyARN); err != nil {
		return nil, err
	}

	user, err := s.getUser(accountID, userName)
	if err != nil {
		return nil, err
	}

	// Idempotent — if already attached, succeed silently
	if slices.Contains(user.AttachedPolicies, policyARN) {
		return &iam.AttachUserPolicyOutput{}, nil
	}

	user.AttachedPolicies = append(user.AttachedPolicies, policyARN)
	userData, err := json.Marshal(user)
	if err != nil {
		return nil, fmt.Errorf("marshal user: %w", err)
	}

	if _, err := s.usersBucket.Put(userKVKey, userData); err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}

	slog.Info("IAM policy attached to user", "accountID", accountID, "userName", userName, "policyArn", policyARN)
	return &iam.AttachUserPolicyOutput{}, nil
}

func (s *IAMServiceImpl) DetachUserPolicy(accountID string, input *iam.DetachUserPolicyInput) (*iam.DetachUserPolicyOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}
	if input.PolicyArn == nil || *input.PolicyArn == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	userName := *input.UserName
	policyARN := *input.PolicyArn
	userKVKey := accountID + ":" + userName

	user, err := s.getUser(accountID, userName)
	if err != nil {
		return nil, err
	}

	found := false
	remaining := make([]string, 0, len(user.AttachedPolicies))
	for _, arn := range user.AttachedPolicies {
		if arn == policyARN {
			found = true
		} else {
			remaining = append(remaining, arn)
		}
	}

	if !found {
		return nil, errors.New(awserrors.ErrorIAMNoSuchEntity)
	}

	user.AttachedPolicies = remaining
	userData, err := json.Marshal(user)
	if err != nil {
		return nil, fmt.Errorf("marshal user: %w", err)
	}

	if _, err := s.usersBucket.Put(userKVKey, userData); err != nil {
		return nil, fmt.Errorf("update user: %w", err)
	}

	slog.Info("IAM policy detached from user", "accountID", accountID, "userName", userName, "policyArn", policyARN)
	return &iam.DetachUserPolicyOutput{}, nil
}

func (s *IAMServiceImpl) ListAttachedUserPolicies(accountID string, input *iam.ListAttachedUserPoliciesInput) (*iam.ListAttachedUserPoliciesOutput, error) {
	if input.UserName == nil || *input.UserName == "" {
		return nil, errors.New(awserrors.ErrorIAMInvalidInput)
	}

	user, err := s.getUser(accountID, *input.UserName)
	if err != nil {
		return nil, err
	}

	var attached []*iam.AttachedPolicy
	for _, arn := range user.AttachedPolicies {
		policy, err := s.getPolicyByARN(accountID, arn)
		if err != nil {
			slog.Warn("ListAttachedUserPolicies: policy not found for ARN", "arn", arn, "err", err)
			continue
		}
		attached = append(attached, &iam.AttachedPolicy{
			PolicyArn:  aws.String(policy.ARN),
			PolicyName: aws.String(policy.PolicyName),
		})
	}

	return &iam.ListAttachedUserPoliciesOutput{
		AttachedPolicies: attached,
		IsTruncated:      aws.Bool(false),
	}, nil
}

// GetUserPolicies resolves all policy documents attached to a user.
// Used internally by the gateway for policy evaluation.
func (s *IAMServiceImpl) GetUserPolicies(accountID, userName string) ([]PolicyDocument, error) {
	user, err := s.getUser(accountID, userName)
	if err != nil {
		return nil, err
	}

	var docs []PolicyDocument
	for _, arn := range user.AttachedPolicies {
		policy, err := s.getPolicyByARN(accountID, arn)
		if err != nil {
			// Fail closed: if we can't resolve a policy, we can't make a safe
			// access decision. Return an error so the caller denies access.
			return nil, fmt.Errorf("resolve policy %s: %w", arn, err)
		}

		doc, err := ValidatePolicyDocument(policy.PolicyDocument)
		if err != nil {
			return nil, fmt.Errorf("parse policy %s: %w", policy.PolicyName, err)
		}
		docs = append(docs, *doc)
	}

	return docs, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (s *IAMServiceImpl) getPolicyByARN(accountID, policyARN string) (*Policy, error) {
	// Extract policy name from ARN: arn:aws:iam::000000000000:policy/path/PolicyName
	parts := strings.SplitN(policyARN, ":policy", 2)
	if len(parts) != 2 || parts[1] == "" {
		return nil, errors.New(awserrors.ErrorIAMNoSuchEntity)
	}
	// The name is the last segment after the final /
	segments := strings.Split(parts[1], "/")
	policyName := segments[len(segments)-1]
	if policyName == "" {
		return nil, errors.New(awserrors.ErrorIAMNoSuchEntity)
	}

	kvKey := accountID + ":" + policyName
	entry, err := s.policiesBucket.Get(kvKey)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, errors.New(awserrors.ErrorIAMNoSuchEntity)
		}
		return nil, fmt.Errorf("get policy: %w", err)
	}

	var policy Policy
	if err := json.Unmarshal(entry.Value(), &policy); err != nil {
		return nil, fmt.Errorf("unmarshal policy: %w", err)
	}

	// Verify the full ARN matches (path may differ)
	if policy.ARN != policyARN {
		return nil, errors.New(awserrors.ErrorIAMNoSuchEntity)
	}

	return &policy, nil
}

// countPolicyAttachments counts how many users in this account have this policy attached.
func (s *IAMServiceImpl) countPolicyAttachments(accountID, policyARN string) (int64, error) {
	keys, err := s.usersBucket.Keys()
	if err != nil {
		if errors.Is(err, nats.ErrNoKeysFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("count policy attachments: %w", err)
	}

	keyPrefix := accountID + ":"
	var count int64
	for _, key := range keys {
		if !strings.HasPrefix(key, keyPrefix) {
			continue
		}

		entry, err := s.usersBucket.Get(key)
		if err != nil {
			if errors.Is(err, nats.ErrKeyNotFound) {
				slog.Debug("countPolicyAttachments: user key disappeared", "key", key)
				continue
			}
			slog.Warn("countPolicyAttachments: failed to get user", "key", key, "err", err)
			continue
		}
		var user User
		if err := json.Unmarshal(entry.Value(), &user); err != nil {
			slog.Warn("countPolicyAttachments: failed to unmarshal user", "key", key, "err", err)
			continue
		}
		if slices.Contains(user.AttachedPolicies, policyARN) {
			count++
		}
	}
	return count, nil
}

func (s *IAMServiceImpl) getUser(accountID, userName string) (*User, error) {
	kvKey := accountID + ":" + userName
	entry, err := s.usersBucket.Get(kvKey)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, errors.New(awserrors.ErrorIAMNoSuchEntity)
		}
		return nil, fmt.Errorf("get user: %w", err)
	}

	var user User
	if err := json.Unmarshal(entry.Value(), &user); err != nil {
		return nil, fmt.Errorf("unmarshal user: %w", err)
	}
	return &user, nil
}

func generateUserID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return "AIDA" + strings.ToUpper(hex.EncodeToString(b))[:17]
}

func generateAccessKeyID() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return "AKIA" + strings.ToUpper(hex.EncodeToString(b))
}

func generateSecretAccessKey() string {
	b := make([]byte, 30)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(b)[:40]
}

func generatePolicyID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return "ANPA" + strings.ToUpper(hex.EncodeToString(b))[:17]
}

// ValidatePolicyDocument parses and validates an IAM policy document JSON string.
func ValidatePolicyDocument(docJSON string) (*PolicyDocument, error) {
	var doc PolicyDocument
	if err := json.Unmarshal([]byte(docJSON), &doc); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	if doc.Version != "2012-10-17" {
		return nil, fmt.Errorf("unsupported policy version: %q", doc.Version)
	}

	if len(doc.Statement) == 0 {
		return nil, fmt.Errorf("policy must contain at least one statement")
	}

	for i, stmt := range doc.Statement {
		if stmt.Effect != "Allow" && stmt.Effect != "Deny" {
			return nil, fmt.Errorf("statement %d: Effect must be Allow or Deny, got %q", i, stmt.Effect)
		}
		if len(stmt.Action) == 0 {
			return nil, fmt.Errorf("statement %d: Action is required", i)
		}
		if len(stmt.Resource) == 0 {
			return nil, fmt.Errorf("statement %d: Resource is required", i)
		}
	}

	return &doc, nil
}
