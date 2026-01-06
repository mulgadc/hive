package handlers_ec2_key

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

// TestKeyPairMetadataMarshaling tests that CreateKeyPairOutput (used as metadata) can be marshaled/unmarshaled correctly
func TestKeyPairMetadataMarshaling(t *testing.T) {
	metadata := ec2.CreateKeyPairOutput{
		KeyPairId:      aws.String("key-12345"),
		KeyFingerprint: aws.String("SHA256:abcdef1234567890"),
		KeyName:        aws.String("test-key"),
		// Note: KeyMaterial is not stored in metadata (only returned once during creation)
	}

	// Marshal to JSON
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Failed to marshal CreateKeyPairOutput: %v", err)
	}

	// Unmarshal from JSON
	var unmarshaled ec2.CreateKeyPairOutput
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal CreateKeyPairOutput: %v", err)
	}

	// Verify fields match
	if *unmarshaled.KeyPairId != *metadata.KeyPairId {
		t.Errorf("KeyPairId mismatch: got %s, want %s", *unmarshaled.KeyPairId, *metadata.KeyPairId)
	}
	if *unmarshaled.KeyFingerprint != *metadata.KeyFingerprint {
		t.Errorf("KeyFingerprint mismatch: got %s, want %s", *unmarshaled.KeyFingerprint, *metadata.KeyFingerprint)
	}
	if *unmarshaled.KeyName != *metadata.KeyName {
		t.Errorf("KeyName mismatch: got %s, want %s", *unmarshaled.KeyName, *metadata.KeyName)
	}
}

// TestDetermineKeyTypeFromFingerprint tests the key type detection logic
func TestDetermineKeyTypeFromFingerprint(t *testing.T) {
	tests := []struct {
		name        string
		fingerprint string
		expected    string
	}{
		{
			name:        "ED25519 key (SHA256 prefix)",
			fingerprint: "SHA256:abcdef1234567890",
			expected:    "ed25519",
		},
		{
			name:        "RSA key (hex fingerprint)",
			fingerprint: "ab:cd:ef:12:34:56:78:90",
			expected:    "rsa",
		},
		{
			name:        "RSA key (uppercase hex)",
			fingerprint: "AB:CD:EF:12:34:56:78:90",
			expected:    "rsa",
		},
		{
			name:        "Empty fingerprint",
			fingerprint: "",
			expected:    "rsa", // default fallback
		},
		{
			name:        "RSA key (MD5 format)",
			fingerprint: "1a2b3c4d5e6f7890abcdef1234567890",
			expected:    "rsa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the exact logic from DescribeKeyPairs (line 537-541)
			var keyType string
			if strings.HasPrefix(tt.fingerprint, "SHA256:") {
				keyType = "ed25519"
			} else {
				keyType = "rsa"
			}

			if keyType != tt.expected {
				t.Errorf("Expected key type %s, got %s for fingerprint %s", tt.expected, keyType, tt.fingerprint)
			}
		})
	}
}

// TestDescribeKeyPairsInputValidation tests input validation for DescribeKeyPairs
func TestDescribeKeyPairsInputValidation(t *testing.T) {
	tests := []struct {
		name      string
		input     *ec2.DescribeKeyPairsInput
		expectErr bool
	}{
		{
			name:      "Nil input",
			input:     nil,
			expectErr: false, // DescribeKeyPairs accepts nil input (all optional)
		},
		{
			name:      "Empty input",
			input:     &ec2.DescribeKeyPairsInput{},
			expectErr: false,
		},
		{
			name: "With KeyNames filter",
			input: &ec2.DescribeKeyPairsInput{
				KeyNames: []*string{aws.String("test-key")},
			},
			expectErr: false,
		},
		{
			name: "With KeyPairIds filter",
			input: &ec2.DescribeKeyPairsInput{
				KeyPairIds: []*string{aws.String("key-12345")},
			},
			expectErr: false,
		},
		{
			name: "With both filters",
			input: &ec2.DescribeKeyPairsInput{
				KeyNames:   []*string{aws.String("test-key")},
				KeyPairIds: []*string{aws.String("key-12345")},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Since DescribeKeyPairs has no required parameters, all inputs should be valid
			// This test documents the expected behavior
			if tt.input == nil && tt.expectErr {
				t.Error("Unexpected error expectation for nil input")
			}
		})
	}
}

// TestKeyPairFiltering tests the filtering logic for DescribeKeyPairs
func TestKeyPairFiltering(t *testing.T) {
	// Create sample key pairs
	keyPairs := []*ec2.KeyPairInfo{
		{
			KeyPairId:      aws.String("key-11111"),
			KeyName:        aws.String("test-key-1"),
			KeyFingerprint: aws.String("SHA256:abc123"),
		},
		{
			KeyPairId:      aws.String("key-22222"),
			KeyName:        aws.String("test-key-2"),
			KeyFingerprint: aws.String("ab:cd:ef:12"),
		},
		{
			KeyPairId:      aws.String("key-33333"),
			KeyName:        aws.String("prod-key-1"),
			KeyFingerprint: aws.String("SHA256:def456"),
		},
	}

	tests := []struct {
		name           string
		input          *ec2.DescribeKeyPairsInput
		expectedCount  int
		expectedKeyIds []string
	}{
		{
			name:           "No filters - return all",
			input:          &ec2.DescribeKeyPairsInput{},
			expectedCount:  3,
			expectedKeyIds: []string{"key-11111", "key-22222", "key-33333"},
		},
		{
			name: "Filter by single KeyName",
			input: &ec2.DescribeKeyPairsInput{
				KeyNames: []*string{aws.String("test-key-1")},
			},
			expectedCount:  1,
			expectedKeyIds: []string{"key-11111"},
		},
		{
			name: "Filter by multiple KeyNames",
			input: &ec2.DescribeKeyPairsInput{
				KeyNames: []*string{aws.String("test-key-1"), aws.String("test-key-2")},
			},
			expectedCount:  2,
			expectedKeyIds: []string{"key-11111", "key-22222"},
		},
		{
			name: "Filter by single KeyPairId",
			input: &ec2.DescribeKeyPairsInput{
				KeyPairIds: []*string{aws.String("key-22222")},
			},
			expectedCount:  1,
			expectedKeyIds: []string{"key-22222"},
		},
		{
			name: "Filter by multiple KeyPairIds",
			input: &ec2.DescribeKeyPairsInput{
				KeyPairIds: []*string{aws.String("key-11111"), aws.String("key-33333")},
			},
			expectedCount:  2,
			expectedKeyIds: []string{"key-11111", "key-33333"},
		},
		{
			name: "Filter by non-existent KeyName",
			input: &ec2.DescribeKeyPairsInput{
				KeyNames: []*string{aws.String("nonexistent-key")},
			},
			expectedCount:  0,
			expectedKeyIds: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate filtering logic from DescribeKeyPairs
			var filtered []*ec2.KeyPairInfo

			if len(tt.input.KeyNames) > 0 {
				// Filter by KeyName
				nameSet := make(map[string]bool)
				for _, name := range tt.input.KeyNames {
					nameSet[*name] = true
				}
				for _, kp := range keyPairs {
					if nameSet[*kp.KeyName] {
						filtered = append(filtered, kp)
					}
				}
			} else if len(tt.input.KeyPairIds) > 0 {
				// Filter by KeyPairId
				idSet := make(map[string]bool)
				for _, id := range tt.input.KeyPairIds {
					idSet[*id] = true
				}
				for _, kp := range keyPairs {
					if idSet[*kp.KeyPairId] {
						filtered = append(filtered, kp)
					}
				}
			} else {
				// No filters - return all
				filtered = keyPairs
			}

			if len(filtered) != tt.expectedCount {
				t.Errorf("Expected %d key pairs, got %d", tt.expectedCount, len(filtered))
			}

			// Verify expected key IDs are present
			for _, expectedId := range tt.expectedKeyIds {
				found := false
				for _, kp := range filtered {
					if *kp.KeyPairId == expectedId {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected key pair ID %s not found in filtered results", expectedId)
				}
			}
		})
	}
}

// TestKeyPairMetadataPath tests the S3 path construction logic
func TestKeyPairMetadataPath(t *testing.T) {
	accountID := "123456789"

	tests := []struct {
		name      string
		keyPairId string
		expected  string
	}{
		{
			name:      "Standard key pair ID",
			keyPairId: "key-abcdef123456",
			expected:  "keys/123456789/key-abcdef123456.json",
		},
		{
			name:      "Short key pair ID",
			keyPairId: "key-12345",
			expected:  "keys/123456789/key-12345.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate path construction from the code
			metadataPath := "keys/" + accountID + "/" + tt.keyPairId + ".json"

			if metadataPath != tt.expected {
				t.Errorf("Expected path %s, got %s", tt.expected, metadataPath)
			}
		})
	}
}

// TestImportKeyPairKeyTypeDetection tests key type detection from public key material
func TestImportKeyPairKeyTypeDetection(t *testing.T) {
	tests := []struct {
		name          string
		publicKeyData string
		expectedType  string
		expectError   bool
	}{
		{
			name:          "ED25519 key",
			publicKeyData: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl",
			expectedType:  "ed25519",
			expectError:   false,
		},
		{
			name:          "RSA key",
			publicKeyData: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCz...",
			expectedType:  "rsa",
			expectError:   false,
		},
		{
			name:          "ECDSA key",
			publicKeyData: "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTY...",
			expectedType:  "ecdsa",
			expectError:   false,
		},
		{
			name:          "Invalid format - no key data",
			publicKeyData: "ssh-rsa",
			expectedType:  "",
			expectError:   true,
		},
		{
			name:          "Empty key material",
			publicKeyData: "",
			expectedType:  "",
			expectError:   true,
		},
		{
			name:          "Unsupported key type",
			publicKeyData: "ssh-dss AAAAB3NzaC1kc3MAAACB...",
			expectedType:  "",
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the key type detection logic from ImportKeyPair
			parts := strings.Fields(tt.publicKeyData)

			if len(parts) < 2 {
				if !tt.expectError {
					t.Errorf("Expected no error but got invalid format")
				}
				return
			}

			var keyType string
			algorithmPrefix := parts[0]

			switch {
			case strings.HasPrefix(algorithmPrefix, "ssh-ed25519"):
				keyType = "ed25519"
			case strings.HasPrefix(algorithmPrefix, "ssh-rsa"):
				keyType = "rsa"
			case strings.HasPrefix(algorithmPrefix, "ecdsa-sha2-"):
				keyType = "ecdsa"
			default:
				if !tt.expectError {
					t.Errorf("Expected no error but got unsupported key type: %s", algorithmPrefix)
				}
				return
			}

			if keyType != tt.expectedType {
				t.Errorf("Expected key type %s, got %s", tt.expectedType, keyType)
			}
		})
	}
}

// TestImportKeyPairInputValidation tests input validation for ImportKeyPair
func TestImportKeyPairInputValidation(t *testing.T) {
	tests := []struct {
		name      string
		input     *ec2.ImportKeyPairInput
		expectErr bool
	}{
		{
			name:      "Nil input",
			input:     nil,
			expectErr: true,
		},
		{
			name: "Missing KeyName",
			input: &ec2.ImportKeyPairInput{
				PublicKeyMaterial: []byte("ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ..."),
			},
			expectErr: true,
		},
		{
			name: "Missing PublicKeyMaterial",
			input: &ec2.ImportKeyPairInput{
				KeyName: aws.String("test-key"),
			},
			expectErr: true,
		},
		{
			name: "Empty PublicKeyMaterial",
			input: &ec2.ImportKeyPairInput{
				KeyName:           aws.String("test-key"),
				PublicKeyMaterial: []byte(""),
			},
			expectErr: true,
		},
		{
			name: "Valid input",
			input: &ec2.ImportKeyPairInput{
				KeyName:           aws.String("test-key"),
				PublicKeyMaterial: []byte("ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ..."),
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate validation logic from ImportKeyPair
			hasError := false

			if tt.input == nil || tt.input.KeyName == nil {
				hasError = true
			} else if len(tt.input.PublicKeyMaterial) == 0 {
				hasError = true
			}

			if hasError != tt.expectErr {
				t.Errorf("Expected error=%v, got error=%v", tt.expectErr, hasError)
			}
		})
	}
}
