package handlers_ec2_key

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/objectstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testBucket    = "test-bucket"
	testAccountID = "123456789"
)

// Valid ed25519 public key for import tests
const testED25519PubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"

// Valid RSA public key for import tests (generated locally, 2048-bit)
const testRSAPubKey = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDP9LrByKWpgbX+prxBwnlf7lrmI5AfDwuiCofuvOAzt9/PwIDMMIAhlvlpm09jjOuuH/MRQApJB5A714Auv+hBKVK0lCq9KcTrnKZOpRj2aGgIZgaoO6P/POoZc+kBf9Y/GP18DCKc4y/XyBsp69dPP6XRdYBlEdeIeVZdgCPYrM7s5FjT7aML2ba2Y2EvH116hPxv+nJZGwM6yqWxWRyTOoNMMTAfNYmoKkNy2zC1FARUyqDwumJ2z5Fvo4ZdN1qoFPOsfPc3iB0NUtSZbLU1awScvHb0rwR5PRnelTZ3Nbkw8I8A0IAhBTE5veW9D38hDIJhRd4nW73BUhgmzDL7"

func newTestKeyService() (*KeyServiceImpl, *objectstore.MemoryObjectStore) {
	store := objectstore.NewMemoryObjectStore()
	svc := NewKeyServiceImplWithStore(store, testBucket, testAccountID)
	return svc, store
}

// requireSSHKeygen skips the test if ssh-keygen is not available.
func requireSSHKeygen(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available, skipping")
	}
}

// importTestKey is a helper that imports an ed25519 key and returns the output.
func importTestKey(t *testing.T, svc *KeyServiceImpl, keyName string) *ec2.ImportKeyPairOutput {
	t.Helper()
	out, err := svc.ImportKeyPair(&ec2.ImportKeyPairInput{
		KeyName:           aws.String(keyName),
		PublicKeyMaterial: []byte(testED25519PubKey),
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	return out
}

// --- TestKeyPairMetadataMarshaling (kept — validates JSON roundtrip independently) ---

func TestKeyPairMetadataMarshaling(t *testing.T) {
	metadata := ec2.CreateKeyPairOutput{
		KeyPairId:      aws.String("key-12345"),
		KeyFingerprint: aws.String("SHA256:abcdef1234567890"),
		KeyName:        aws.String("test-key"),
	}

	data, err := json.Marshal(metadata)
	require.NoError(t, err)

	var unmarshaled ec2.CreateKeyPairOutput
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, *metadata.KeyPairId, *unmarshaled.KeyPairId)
	assert.Equal(t, *metadata.KeyFingerprint, *unmarshaled.KeyFingerprint)
	assert.Equal(t, *metadata.KeyName, *unmarshaled.KeyName)
}

// ============================================================
// CreateKeyPair Tests
// ============================================================

func TestCreateKeyPair_ED25519(t *testing.T) {
	requireSSHKeygen(t)
	svc, store := newTestKeyService()

	out, err := svc.CreateKeyPair(&ec2.CreateKeyPairInput{
		KeyName: aws.String("my-ed25519-key"),
	})
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.Equal(t, "my-ed25519-key", *out.KeyName)
	assert.NotEmpty(t, *out.KeyPairId)
	assert.True(t, strings.HasPrefix(*out.KeyPairId, "key-"))
	assert.NotEmpty(t, *out.KeyFingerprint)
	assert.True(t, strings.HasPrefix(*out.KeyFingerprint, "SHA256:"), "ed25519 fingerprint should be SHA256 format")
	assert.NotEmpty(t, *out.KeyMaterial)
	assert.Contains(t, *out.KeyMaterial, "PRIVATE KEY")

	// Verify public key stored in S3
	keyPath := "keys/" + testAccountID + "/my-ed25519-key"
	getOut, err := store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String(keyPath),
	})
	require.NoError(t, err)
	assert.NotNil(t, getOut)

	// Verify metadata stored in S3
	metaPath := "keys/" + testAccountID + "/" + *out.KeyPairId + ".json"
	metaOut, err := store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String(metaPath),
	})
	require.NoError(t, err)
	assert.NotNil(t, metaOut)
}

func TestCreateKeyPair_RSA(t *testing.T) {
	requireSSHKeygen(t)
	svc, _ := newTestKeyService()

	out, err := svc.CreateKeyPair(&ec2.CreateKeyPairInput{
		KeyName: aws.String("my-rsa-key"),
		KeyType: aws.String("rsa"),
	})
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.Equal(t, "my-rsa-key", *out.KeyName)
	assert.NotEmpty(t, *out.KeyFingerprint)
	// RSA fingerprint is MD5 hex (no "SHA256:" prefix, no colons in our format)
	assert.False(t, strings.HasPrefix(*out.KeyFingerprint, "SHA256:"), "RSA fingerprint should not be SHA256 format")
}

func TestCreateKeyPair_NilInput(t *testing.T) {
	svc, _ := newTestKeyService()

	out, err := svc.CreateKeyPair(nil)
	require.Error(t, err)
	assert.Nil(t, out)
	assert.Equal(t, awserrors.ErrorMissingParameter, err.Error())
}

func TestCreateKeyPair_MissingKeyName(t *testing.T) {
	svc, _ := newTestKeyService()

	out, err := svc.CreateKeyPair(&ec2.CreateKeyPairInput{})
	require.Error(t, err)
	assert.Nil(t, out)
	assert.Equal(t, awserrors.ErrorMissingParameter, err.Error())
}

func TestCreateKeyPair_InvalidKeyName(t *testing.T) {
	svc, _ := newTestKeyService()

	out, err := svc.CreateKeyPair(&ec2.CreateKeyPairInput{
		KeyName: aws.String("invalid key name!@#"),
	})
	require.Error(t, err)
	assert.Nil(t, out)
	assert.Equal(t, awserrors.ErrorInvalidKeyPairFormat, err.Error())
}

func TestCreateKeyPair_Duplicate(t *testing.T) {
	requireSSHKeygen(t)
	svc, _ := newTestKeyService()

	_, err := svc.CreateKeyPair(&ec2.CreateKeyPairInput{
		KeyName: aws.String("dup-key"),
	})
	require.NoError(t, err)

	out, err := svc.CreateKeyPair(&ec2.CreateKeyPairInput{
		KeyName: aws.String("dup-key"),
	})
	require.Error(t, err)
	assert.Nil(t, out)
	assert.Equal(t, awserrors.ErrorInvalidKeyPairDuplicate, err.Error())
}

func TestCreateKeyPair_InvalidKeyType(t *testing.T) {
	svc, _ := newTestKeyService()

	out, err := svc.CreateKeyPair(&ec2.CreateKeyPairInput{
		KeyName: aws.String("bad-type-key"),
		KeyType: aws.String("dsa"),
	})
	require.Error(t, err)
	assert.Nil(t, out)
	assert.Equal(t, awserrors.ErrorInvalidParameterValue, err.Error())
}

// ============================================================
// ImportKeyPair Tests
// ============================================================

func TestImportKeyPair_Success_ED25519(t *testing.T) {
	svc, store := newTestKeyService()

	out, err := svc.ImportKeyPair(&ec2.ImportKeyPairInput{
		KeyName:           aws.String("imported-ed25519"),
		PublicKeyMaterial: []byte(testED25519PubKey),
	})
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.Equal(t, "imported-ed25519", *out.KeyName)
	assert.NotEmpty(t, *out.KeyPairId)
	assert.True(t, strings.HasPrefix(*out.KeyPairId, "key-"))
	assert.NotEmpty(t, *out.KeyFingerprint)
	assert.True(t, strings.HasPrefix(*out.KeyFingerprint, "SHA256:"))

	// Verify public key stored in S3
	keyPath := "keys/" + testAccountID + "/imported-ed25519"
	getOut, err := store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String(keyPath),
	})
	require.NoError(t, err)
	assert.NotNil(t, getOut)

	// Verify metadata stored in S3
	metaPath := "keys/" + testAccountID + "/" + *out.KeyPairId + ".json"
	metaOut, err := store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String(metaPath),
	})
	require.NoError(t, err)
	assert.NotNil(t, metaOut)
}

func TestImportKeyPair_Success_RSA(t *testing.T) {
	svc, _ := newTestKeyService()

	out, err := svc.ImportKeyPair(&ec2.ImportKeyPairInput{
		KeyName:           aws.String("imported-rsa"),
		PublicKeyMaterial: []byte(testRSAPubKey),
	})
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.Equal(t, "imported-rsa", *out.KeyName)
	assert.NotEmpty(t, *out.KeyFingerprint)
	assert.False(t, strings.HasPrefix(*out.KeyFingerprint, "SHA256:"), "RSA fingerprint should be MD5 format")
}

func TestImportKeyPair_Duplicate(t *testing.T) {
	svc, _ := newTestKeyService()

	_, err := svc.ImportKeyPair(&ec2.ImportKeyPairInput{
		KeyName:           aws.String("dup-import"),
		PublicKeyMaterial: []byte(testED25519PubKey),
	})
	require.NoError(t, err)

	out, err := svc.ImportKeyPair(&ec2.ImportKeyPairInput{
		KeyName:           aws.String("dup-import"),
		PublicKeyMaterial: []byte(testED25519PubKey),
	})
	require.Error(t, err)
	assert.Nil(t, out)
	assert.Equal(t, awserrors.ErrorInvalidKeyPairDuplicate, err.Error())
}

func TestImportKeyPair_InvalidKeyName(t *testing.T) {
	svc, _ := newTestKeyService()

	out, err := svc.ImportKeyPair(&ec2.ImportKeyPairInput{
		KeyName:           aws.String("bad name with spaces!"),
		PublicKeyMaterial: []byte(testED25519PubKey),
	})
	require.Error(t, err)
	assert.Nil(t, out)
	assert.Equal(t, awserrors.ErrorInvalidKeyPairFormat, err.Error())
}

func TestImportKeyPairInvalidKeyFormat(t *testing.T) {
	svc, _ := newTestKeyService()

	tests := []struct {
		name           string
		publicKey      string
		expectedErrMsg string
	}{
		{
			name:           "SingleFieldNoKeyData",
			publicKey:      "ssh-rsa",
			expectedErrMsg: awserrors.ErrorInvalidKeyFormat,
		},
		{
			name:           "UnsupportedAlgorithm",
			publicKey:      "ssh-dss AAAAB3NzaC1kc3MAAACB",
			expectedErrMsg: awserrors.ErrorInvalidKeyFormat,
		},
		{
			name:           "InvalidBase64",
			publicKey:      "ssh-rsa not-valid-base64!!!",
			expectedErrMsg: awserrors.ErrorInvalidKeyFormat,
		},
		{
			name:           "EmptyKeyData",
			publicKey:      "ssh-ed25519 ",
			expectedErrMsg: awserrors.ErrorInvalidKeyFormat,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.ImportKeyPair(&ec2.ImportKeyPairInput{
				KeyName:           aws.String("test-key"),
				PublicKeyMaterial: []byte(tt.publicKey),
			})
			require.Error(t, err)
			assert.Equal(t, tt.expectedErrMsg, err.Error())
		})
	}
}

// ============================================================
// DeleteKeyPair Tests
// ============================================================

func TestDeleteKeyPair_ByKeyName(t *testing.T) {
	svc, store := newTestKeyService()

	imported := importTestKey(t, svc, "to-delete-by-name")

	result, err := svc.DeleteKeyPair(&ec2.DeleteKeyPairInput{
		KeyName: aws.String("to-delete-by-name"),
	})
	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify public key removed from S3
	keyPath := "keys/" + testAccountID + "/to-delete-by-name"
	_, err = store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String(keyPath),
	})
	assert.Error(t, err, "public key should be deleted")

	// Verify metadata removed from S3
	metaPath := "keys/" + testAccountID + "/" + *imported.KeyPairId + ".json"
	_, err = store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String(metaPath),
	})
	assert.Error(t, err, "metadata should be deleted")
}

func TestDeleteKeyPair_ByKeyPairId(t *testing.T) {
	svc, store := newTestKeyService()

	imported := importTestKey(t, svc, "to-delete-by-id")

	result, err := svc.DeleteKeyPair(&ec2.DeleteKeyPairInput{
		KeyPairId: imported.KeyPairId,
	})
	require.NoError(t, err)
	assert.NotNil(t, result)

	// Verify public key removed from S3
	keyPath := "keys/" + testAccountID + "/to-delete-by-id"
	_, err = store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String(keyPath),
	})
	assert.Error(t, err, "public key should be deleted")

	// Verify metadata removed from S3
	metaPath := "keys/" + testAccountID + "/" + *imported.KeyPairId + ".json"
	_, err = store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String(metaPath),
	})
	assert.Error(t, err, "metadata should be deleted")
}

func TestDeleteKeyPairIdempotent(t *testing.T) {
	svc, _ := newTestKeyService()

	t.Run("NonExistentKeyName", func(t *testing.T) {
		result, err := svc.DeleteKeyPair(&ec2.DeleteKeyPairInput{
			KeyName: aws.String("no-such-key"),
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("NonExistentKeyPairId", func(t *testing.T) {
		result, err := svc.DeleteKeyPair(&ec2.DeleteKeyPairInput{
			KeyPairId: aws.String("key-0123456789abcdef0"),
		})
		require.NoError(t, err)
		assert.NotNil(t, result)
	})
}

func TestDeleteKeyPair_NilInput(t *testing.T) {
	svc, _ := newTestKeyService()

	result, err := svc.DeleteKeyPair(nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, awserrors.ErrorMissingParameter, err.Error())
}

func TestDeleteKeyPair_EmptyNameAndId(t *testing.T) {
	svc, _ := newTestKeyService()

	result, err := svc.DeleteKeyPair(&ec2.DeleteKeyPairInput{})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, awserrors.ErrorMissingParameter, err.Error())
}

func TestDeleteKeyPair_InvalidKeyPairIdFormat(t *testing.T) {
	svc, _ := newTestKeyService()

	result, err := svc.DeleteKeyPair(&ec2.DeleteKeyPairInput{
		KeyPairId: aws.String("bad id format!!!"),
	})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, awserrors.ErrorInvalidKeyPairFormat, err.Error())
}

// ============================================================
// DescribeKeyPairs Tests
// ============================================================

func TestDescribeKeyPairs_Empty(t *testing.T) {
	svc, _ := newTestKeyService()

	out, err := svc.DescribeKeyPairs(&ec2.DescribeKeyPairsInput{})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Empty(t, out.KeyPairs)
}

func TestDescribeKeyPairs_AllKeys(t *testing.T) {
	svc, _ := newTestKeyService()

	importTestKey(t, svc, "key-alpha")
	importTestKey(t, svc, "key-beta")

	out, err := svc.DescribeKeyPairs(&ec2.DescribeKeyPairsInput{})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Len(t, out.KeyPairs, 2)

	names := make(map[string]bool)
	for _, kp := range out.KeyPairs {
		names[*kp.KeyName] = true
		assert.NotEmpty(t, *kp.KeyPairId)
		assert.NotEmpty(t, *kp.KeyFingerprint)
		assert.NotEmpty(t, *kp.KeyType)
	}
	assert.True(t, names["key-alpha"])
	assert.True(t, names["key-beta"])
}

func TestDescribeKeyPairs_FilterByKeyName(t *testing.T) {
	svc, _ := newTestKeyService()

	importTestKey(t, svc, "find-me")
	importTestKey(t, svc, "ignore-me")

	out, err := svc.DescribeKeyPairs(&ec2.DescribeKeyPairsInput{
		KeyNames: []*string{aws.String("find-me")},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Len(t, out.KeyPairs, 1)
	assert.Equal(t, "find-me", *out.KeyPairs[0].KeyName)
}

func TestDescribeKeyPairs_FilterByKeyPairId(t *testing.T) {
	svc, _ := newTestKeyService()

	imported := importTestKey(t, svc, "find-by-id")
	importTestKey(t, svc, "other-key")

	out, err := svc.DescribeKeyPairs(&ec2.DescribeKeyPairsInput{
		KeyPairIds: []*string{imported.KeyPairId},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Len(t, out.KeyPairs, 1)
	assert.Equal(t, "find-by-id", *out.KeyPairs[0].KeyName)
}

func TestDescribeKeyPairs_FilterNoMatch(t *testing.T) {
	svc, _ := newTestKeyService()

	importTestKey(t, svc, "exists")

	out, err := svc.DescribeKeyPairs(&ec2.DescribeKeyPairsInput{
		KeyNames: []*string{aws.String("does-not-exist")},
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Empty(t, out.KeyPairs)
}

func TestDescribeKeyPairs_NilInput(t *testing.T) {
	svc, _ := newTestKeyService()

	out, err := svc.DescribeKeyPairs(nil)
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Empty(t, out.KeyPairs)
}

// ============================================================
// ValidateKeyPairExists Tests
// ============================================================

func TestValidateKeyPairExists_Found(t *testing.T) {
	svc, _ := newTestKeyService()

	importTestKey(t, svc, "existing-key")

	err := svc.ValidateKeyPairExists("existing-key")
	assert.NoError(t, err)
}

func TestValidateKeyPairExists_NotFound(t *testing.T) {
	svc, _ := newTestKeyService()

	err := svc.ValidateKeyPairExists("ghost-key")
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidKeyPairNotFound, err.Error())
}
