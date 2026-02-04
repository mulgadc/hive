package handlers_ec2_key

import (
	"bytes"
	"crypto/md5"  //#nosec G501 - need md5 for AWS compatibility
	"crypto/sha1" //#nosec G505 - need sha256 for AWS compatibility
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/handlers/ec2/objectstore"
	"github.com/mulgadc/hive/hive/utils"
)

// KeyServiceImpl handles key pair operations with ssh-keygen and S3 storage
type KeyServiceImpl struct {
	config     *config.Config
	store      objectstore.ObjectStore
	bucketName string
	accountID  string // AWS account ID for S3 key storage path
}

// NewKeyServiceImpl creates a new daemon-side key service
func NewKeyServiceImpl(cfg *config.Config) *KeyServiceImpl {
	store := objectstore.NewS3ObjectStoreFromConfig(
		cfg.Predastore.Host,
		cfg.Predastore.Region,
		cfg.Predastore.AccessKey,
		cfg.Predastore.SecretKey,
	)

	return &KeyServiceImpl{
		config:     cfg,
		store:      store,
		bucketName: cfg.Predastore.Bucket,
		accountID:  "123456789", // TODO: Implement proper account ID management
	}
}

// NewKeyServiceImplWithStore creates a key service with a custom object store (for testing)
func NewKeyServiceImplWithStore(store objectstore.ObjectStore, bucketName, accountID string) *KeyServiceImpl {
	return &KeyServiceImpl{
		store:      store,
		bucketName: bucketName,
		accountID:  accountID,
	}
}

// CreateKeyPair generates a new SSH key pair using ssh-keygen
func (s *KeyServiceImpl) CreateKeyPair(input *ec2.CreateKeyPairInput) (*ec2.CreateKeyPairOutput, error) {
	if input == nil || input.KeyName == nil {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	keyName := *input.KeyName
	slog.Info("Creating key pair", "keyName", keyName)

	// Validate key name contains only allowed characters
	if err := utils.ValidateKeyPairName(keyName); err != nil {
		slog.Error("Invalid key pair name", "keyName", keyName, "err", err)
		return nil, errors.New(awserrors.ErrorInvalidKeyPairFormat)
	}

	// Check if key already exists in S3
	keyPath := fmt.Sprintf("keys/%s/%s", s.accountID, keyName)
	_, err := s.store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(keyPath),
	})

	if err == nil {
		// Object exists - return duplicate error
		slog.Error("Key pair already exists", "keyName", keyName)
		return nil, errors.New(awserrors.ErrorInvalidKeyPairDuplicate)
	}

	// Determine key type (default: ed25519, optional: rsa)
	keyType := "ed25519"
	if input.KeyType != nil {
		switch *input.KeyType {
		case "rsa":
			keyType = "rsa"
		case "ed25519":
			keyType = "ed25519"
		default:
			return nil, errors.New("InvalidParameterValue")
		}
	}

	// Create temporary directory for key generation
	tmpDir, err := os.MkdirTemp("", "hive-keypair-*")
	if err != nil {
		slog.Error("Failed to create temp directory", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	defer os.RemoveAll(tmpDir)

	privateKeyPath := filepath.Join(tmpDir, "id_key")
	publicKeyPath := privateKeyPath + ".pub"

	// Generate key pair using ssh-keygen
	var cmd *exec.Cmd
	if keyType == "ed25519" {
		// ED25519 key (modern, recommended)
		cmd = exec.Command("ssh-keygen", "-t", "ed25519", "-f", privateKeyPath, "-N", "", "-C", "")
	} else {
		// RSA 2048-bit key
		cmd = exec.Command("ssh-keygen", "-t", "rsa", "-b", "2048", "-f", privateKeyPath, "-N", "", "-C", "")
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		slog.Error("ssh-keygen failed", "err", err, "stderr", stderr.String())
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Read private key
	privateKeyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		slog.Error("Failed to read private key", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Read public key
	publicKeyData, err := os.ReadFile(publicKeyPath)
	if err != nil {
		slog.Error("Failed to read public key", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Calculate fingerprint based on key type
	fingerprint, err := s.calculateFingerprint(publicKeyData, keyType)
	if err != nil {
		slog.Error("Failed to calculate fingerprint", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Upload public key to S3
	_, err = s.store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(keyPath),
		Body:   bytes.NewReader(publicKeyData),
	})
	if err != nil {
		slog.Error("Failed to upload public key to S3", "err", err, "path", keyPath)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Build response (similar to AWS EC2)
	keyPairID := fmt.Sprintf("key-%s", generateKeyPairID())
	output := &ec2.CreateKeyPairOutput{
		KeyFingerprint: aws.String(fingerprint),
		KeyMaterial:    aws.String(string(privateKeyData)),
		KeyName:        aws.String(keyName),
		KeyPairId:      aws.String(keyPairID),
	}

	// Store metadata file (CreateKeyPairOutput without KeyMaterial) for keyPairId lookups
	err = s.storeKeyPairMetadata(keyPairID, &ec2.CreateKeyPairOutput{
		KeyFingerprint: aws.String(fingerprint),
		KeyName:        aws.String(keyName),
		KeyPairId:      aws.String(keyPairID),
	})
	if err != nil {
		slog.Error("Failed to store key pair metadata", "err", err, "keyPairId", keyPairID)
		// Try to cleanup the public key we just uploaded
		s.store.DeleteObject(&s3.DeleteObjectInput{
			Bucket: aws.String(s.bucketName),
			Key:    aws.String(keyPath),
		})
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("Key pair created successfully", "keyName", keyName, "fingerprint", fingerprint, "keyPairId", keyPairID)

	return output, nil
}

// calculateFingerprint computes the SSH key fingerprint
// - For RSA: SHA-1 hash of public key (MD5 for older format)
// - For ED25519: SHA-256 hash of public key
func (s *KeyServiceImpl) calculateFingerprint(publicKeyData []byte, keyType string) (string, error) {
	// Parse the public key to extract the key data
	// Format: "ssh-ed25519 AAAAC3Nza... comment"
	parts := strings.Fields(string(publicKeyData))
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid public key format")
	}

	// Decode base64 key data
	keyData, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("failed to decode public key: %w", err)
	}

	if keyType == "ed25519" {
		// ED25519 uses SHA-256 fingerprint
		hash := sha256.Sum256(keyData)
		return formatFingerprint(hash[:], "SHA256"), nil
	} else {
		// RSA uses SHA-1 or MD5 fingerprint
		// AWS uses MD5 for RSA keys for backward compatibility
		hash := md5.Sum(keyData) //#nosec G401 - need md5 for AWS compatibility
		return formatFingerprint(hash[:], "MD5"), nil
	}
}

// formatFingerprint formats the hash as a colon-separated hex string
func formatFingerprint(hash []byte, algorithm string) string {
	if algorithm == "MD5" {
		// MD5 format: aa:bb:cc:dd:...
		return strings.ToLower(hex.EncodeToString(hash))
	} else {
		// SHA256 format: SHA256:base64encodedstring
		return fmt.Sprintf("SHA256:%s", base64.RawStdEncoding.EncodeToString(hash))
	}
}

// generateKeyPairID generates a unique key pair ID (similar to AWS key-xxxxx format)
func generateKeyPairID() string {
	hash := sha1.New() //#nosec G401 - need sha1 for AWS compatibility
	hash.Write(fmt.Appendf(nil, "%d", time.Now().UnixNano()))
	return fmt.Sprintf("%x", hash.Sum(nil))[:16]
}

// storeKeyPairMetadata stores key pair metadata (without private key) to S3 for keyPairId lookups
func (s *KeyServiceImpl) storeKeyPairMetadata(keyPairID string, metadata *ec2.CreateKeyPairOutput) error {
	// Store metadata with keyPairId as filename for efficient lookup when keyPairId is provided
	metadataPath := fmt.Sprintf("keys/%s/%s.json", s.accountID, keyPairID)

	// Marshal metadata to JSON
	jsonData, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Upload metadata to S3
	_, err = s.store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(metadataPath),
		Body:   bytes.NewReader(jsonData),
	})
	if err != nil {
		return fmt.Errorf("failed to upload metadata: %w", err)
	}

	return nil
}

// getKeyNameFromKeyPairId retrieves the key name by directly reading the metadata file for a given keyPairId
func (s *KeyServiceImpl) getKeyNameFromKeyPairId(keyPairID string) (string, error) {
	metadataPath := fmt.Sprintf("keys/%s/%s.json", s.accountID, keyPairID)

	// Get metadata from S3
	result, err := s.store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(metadataPath),
	})
	if err != nil {
		if objectstore.IsNoSuchKeyError(err) {
			return "", errors.New(awserrors.ErrorInvalidKeyPairNotFound)
		}
		slog.Error("Failed to get key pair metadata", "keyPairId", keyPairID, "err", err)
		return "", fmt.Errorf("failed to get metadata: %w", err)
	}
	defer result.Body.Close()

	// Read and parse metadata
	body, err := io.ReadAll(result.Body)
	if err != nil {
		slog.Error("Failed to read metadata body", "err", err)
		return "", fmt.Errorf("failed to read metadata: %w", err)
	}

	var metadata ec2.CreateKeyPairOutput
	if err := json.Unmarshal(body, &metadata); err != nil {
		slog.Error("Failed to unmarshal metadata", "err", err)
		return "", fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	if metadata.KeyName == nil {
		slog.Error("Metadata missing KeyName field", "keyPairId", keyPairID)
		return "", fmt.Errorf("invalid metadata: missing KeyName")
	}

	return *metadata.KeyName, nil
}

// findKeyPairIdFromKeyName finds the keyPairId by searching metadata files for a given keyName
func (s *KeyServiceImpl) findKeyPairIdFromKeyName(keyName string) (string, error) {
	prefix := fmt.Sprintf("keys/%s/", s.accountID)

	// List all objects with the keys prefix
	result, err := s.store.ListObjects(&s3.ListObjectsInput{
		Bucket: aws.String(s.bucketName),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		slog.Error("Failed to list S3 objects", "prefix", prefix, "err", err)
		return "", fmt.Errorf("failed to list objects: %w", err)
	}

	// Check each .json metadata file
	for _, obj := range result.Contents {
		if obj.Key == nil {
			continue
		}

		// Only check .json files (metadata files)
		if !strings.HasSuffix(*obj.Key, ".json") {
			continue
		}

		// Get the metadata file
		// TODO: Have a more elegant solution, temporary until we have a proper key/value DB
		getResult, err := s.store.GetObject(&s3.GetObjectInput{
			Bucket: aws.String(s.bucketName),
			Key:    obj.Key,
		})
		if err != nil {
			slog.Debug("Failed to get metadata file", "key", *obj.Key, "err", err)
			continue
		}

		body, err := io.ReadAll(getResult.Body)
		getResult.Body.Close()
		if err != nil {
			slog.Debug("Failed to read metadata body", "key", *obj.Key, "err", err)
			continue
		}

		var metadata ec2.CreateKeyPairOutput
		if err := json.Unmarshal(body, &metadata); err != nil {
			slog.Debug("Failed to unmarshal metadata", "key", *obj.Key, "err", err)
			continue
		}

		// Check if this metadata matches the keyName
		if metadata.KeyName != nil && *metadata.KeyName == keyName {
			if metadata.KeyPairId != nil {
				return *metadata.KeyPairId, nil
			}
		}
	}

	// Key pair not found
	return "", errors.New(awserrors.ErrorInvalidKeyPairNotFound)
}

// DeleteKeyPair removes a key pair (both public key and metadata from S3)
func (s *KeyServiceImpl) DeleteKeyPair(input *ec2.DeleteKeyPairInput) (*ec2.DeleteKeyPairOutput, error) {
	if input == nil {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	var keyName string
	var keyPairID string
	var err error

	// Determine keyName and keyPairId from input
	if input.KeyPairId != nil && *input.KeyPairId != "" {
		// KeyPairId provided - validate and look up keyName from metadata
		keyPairID = *input.KeyPairId

		// Validate keyPairId format (strip "key-" prefix before validation)
		keyPairIDStripped := strings.TrimPrefix(keyPairID, "key-")
		if err := utils.ValidateKeyPairName(keyPairIDStripped); err != nil {
			slog.Error("Invalid key pair ID format", "keyPairId", keyPairID, "err", err)
			return nil, errors.New(awserrors.ErrorInvalidKeyPairFormat)
		}

		keyName, err = s.getKeyNameFromKeyPairId(keyPairID)
		if err != nil {
			slog.Error("Failed to get keyName from keyPairId", "keyPairId", keyPairID, "err", err)
			return nil, err
		}
	} else if input.KeyName != nil && *input.KeyName != "" {
		// KeyName provided - validate and find the keyPairId
		keyName = *input.KeyName

		// Validate keyName format
		if err := utils.ValidateKeyPairName(keyName); err != nil {
			slog.Error("Invalid key pair name format", "keyName", keyName, "err", err)
			return nil, errors.New(awserrors.ErrorInvalidKeyPairFormat)
		}

		keyPairID, err = s.findKeyPairIdFromKeyName(keyName)
		if err != nil {
			slog.Error("Failed to find keyPairId from keyName", "keyName", keyName, "err", err)
			return nil, err
		}
	} else {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	slog.Info("Deleting key pair", "keyName", keyName, "keyPairId", keyPairID)

	// Delete public key
	publicKeyPath := fmt.Sprintf("keys/%s/%s", s.accountID, keyName)
	_, err = s.store.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(publicKeyPath),
	})
	if err != nil {
		slog.Error("Failed to delete public key", "path", publicKeyPath, "err", err)
		// Continue to try deleting metadata even if public key deletion fails
	}

	// Delete metadata file (stored with keyPairID)
	metadataPath := fmt.Sprintf("keys/%s/%s.json", s.accountID, keyPairID)
	_, err = s.store.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(metadataPath),
	})
	if err != nil {
		slog.Error("Failed to delete metadata", "path", metadataPath, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("Key pair deleted successfully", "keyName", keyName, "keyPairId", keyPairID)

	return &ec2.DeleteKeyPairOutput{}, nil
}

// DescribeKeyPairs lists available key pairs by reading metadata files from S3
func (s *KeyServiceImpl) DescribeKeyPairs(input *ec2.DescribeKeyPairsInput) (*ec2.DescribeKeyPairsOutput, error) {
	if input == nil {
		input = &ec2.DescribeKeyPairsInput{}
	}

	slog.Info("Describing key pairs", "filters", input.Filters)

	prefix := fmt.Sprintf("keys/%s/", s.accountID)

	// List all objects with the keys prefix
	result, err := s.store.ListObjects(&s3.ListObjectsInput{
		Bucket: aws.String(s.bucketName),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		slog.Error("Failed to list S3 objects", "prefix", prefix, "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	var keyPairs []*ec2.KeyPairInfo

	// Check each .json metadata file
	for _, obj := range result.Contents {
		if obj.Key == nil {
			continue
		}

		// Only check .json files (metadata files)
		if !strings.HasSuffix(*obj.Key, ".json") {
			continue
		}

		// Get the metadata file
		getResult, err := s.store.GetObject(&s3.GetObjectInput{
			Bucket: aws.String(s.bucketName),
			Key:    obj.Key,
		})
		if err != nil {
			slog.Debug("Failed to get metadata file", "key", *obj.Key, "err", err)
			continue
		}

		body, err := io.ReadAll(getResult.Body)
		getResult.Body.Close()
		if err != nil {
			slog.Debug("Failed to read metadata body", "key", *obj.Key, "err", err)
			continue
		}

		var metadata ec2.CreateKeyPairOutput
		if err := json.Unmarshal(body, &metadata); err != nil {
			slog.Debug("Failed to unmarshal metadata", "key", *obj.Key, "err", err)
			continue
		}

		// Filter by KeyName if specified
		if len(input.KeyNames) > 0 {
			found := false
			for _, filterName := range input.KeyNames {
				if filterName != nil && metadata.KeyName != nil && *filterName == *metadata.KeyName {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Filter by KeyPairId if specified
		if len(input.KeyPairIds) > 0 {
			found := false
			for _, filterID := range input.KeyPairIds {
				if filterID != nil && metadata.KeyPairId != nil && *filterID == *metadata.KeyPairId {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Determine key type from fingerprint format
		var keyType string
		if metadata.KeyFingerprint != nil && strings.HasPrefix(*metadata.KeyFingerprint, "SHA256:") {
			keyType = "ed25519"
		} else {
			keyType = "rsa"
		}

		// Build KeyPairInfo from metadata
		// Note: CreateTime would need to be stored in metadata or use S3 object LastModified
		keyPairInfo := &ec2.KeyPairInfo{
			KeyPairId:      metadata.KeyPairId,
			KeyFingerprint: metadata.KeyFingerprint,
			KeyName:        metadata.KeyName,
			KeyType:        aws.String(keyType),
			Tags:           []*ec2.Tag{}, // TODO: Implement tag support
		}

		// Use S3 object LastModified as CreateTime
		if obj.LastModified != nil {
			keyPairInfo.CreateTime = obj.LastModified
		}

		keyPairs = append(keyPairs, keyPairInfo)
	}

	slog.Info("DescribeKeyPairs completed", "count", len(keyPairs))

	return &ec2.DescribeKeyPairsOutput{
		KeyPairs: keyPairs,
	}, nil
}

// ImportKeyPair imports an existing public key
func (s *KeyServiceImpl) ImportKeyPair(input *ec2.ImportKeyPairInput) (*ec2.ImportKeyPairOutput, error) {
	if input == nil || input.KeyName == nil {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	if len(input.PublicKeyMaterial) == 0 {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	keyName := *input.KeyName
	slog.Info("Importing key pair", "keyName", keyName)

	// Validate key name contains only allowed characters
	if err := utils.ValidateKeyPairName(keyName); err != nil {
		slog.Error("Invalid key pair name", "keyName", keyName, "err", err)
		return nil, errors.New(awserrors.ErrorInvalidKeyPairFormat)
	}

	// Check if key already exists in S3
	keyPath := fmt.Sprintf("keys/%s/%s", s.accountID, keyName)
	_, err := s.store.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(keyPath),
	})

	if err == nil {
		// Object exists - return duplicate error
		slog.Error("Key pair already exists", "keyName", keyName)
		return nil, errors.New(awserrors.ErrorInvalidKeyPairDuplicate)
	}

	// Parse the public key material to extract key data and determine type
	publicKeyData := input.PublicKeyMaterial
	publicKeyString := string(publicKeyData)

	// Parse the public key format: "ssh-rsa AAAAB..." or "ssh-ed25519 AAAAC..."
	parts := strings.Fields(publicKeyString)
	if len(parts) < 2 {
		slog.Error("Invalid public key format", "keyName", keyName)
		return nil, errors.New(awserrors.ErrorInvalidKeyPairFormat)
	}

	// Determine key type from algorithm prefix
	var keyType string
	algorithmPrefix := parts[0]
	switch {
	case strings.HasPrefix(algorithmPrefix, "ssh-ed25519"):
		keyType = "ed25519"
	case strings.HasPrefix(algorithmPrefix, "ssh-rsa"):
		keyType = "rsa"
	case strings.HasPrefix(algorithmPrefix, "ecdsa-sha2-"):
		// ECDSA keys are also supported but less common
		keyType = "ecdsa"
	default:
		slog.Error("Unsupported key type", "algorithm", algorithmPrefix, "keyName", keyName)
		return nil, errors.New(awserrors.ErrorInvalidKeyPairFormat)
	}

	// Calculate fingerprint from the imported public key
	fingerprint, err := s.calculateFingerprint(publicKeyData, keyType)
	if err != nil {
		slog.Error("Failed to calculate fingerprint", "err", err)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Upload public key to S3
	_, err = s.store.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(keyPath),
		Body:   bytes.NewReader(publicKeyData),
	})
	if err != nil {
		slog.Error("Failed to upload public key to S3", "err", err, "path", keyPath)
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Generate key pair ID
	keyPairID := fmt.Sprintf("key-%s", generateKeyPairID())

	// Build response output
	output := &ec2.ImportKeyPairOutput{
		KeyFingerprint: aws.String(fingerprint),
		KeyName:        aws.String(keyName),
		KeyPairId:      aws.String(keyPairID),
		Tags:           []*ec2.Tag{}, // TODO: Implement tag support from input.TagSpecifications
	}

	// Store metadata file (without public key material)
	metadataOutput := &ec2.CreateKeyPairOutput{
		KeyFingerprint: aws.String(fingerprint),
		KeyName:        aws.String(keyName),
		KeyPairId:      aws.String(keyPairID),
	}

	err = s.storeKeyPairMetadata(keyPairID, metadataOutput)
	if err != nil {
		slog.Error("Failed to store key pair metadata", "err", err, "keyPairId", keyPairID)
		// Try to cleanup the public key we just uploaded
		s.store.DeleteObject(&s3.DeleteObjectInput{
			Bucket: aws.String(s.bucketName),
			Key:    aws.String(keyPath),
		})
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("Key pair imported successfully", "keyName", keyName, "fingerprint", fingerprint, "keyPairId", keyPairID, "keyType", keyType)

	return output, nil
}
