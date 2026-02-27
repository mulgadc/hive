package handlers_iam

import "encoding/json"

// User represents an IAM user stored in JetStream KV.
type User struct {
	UserName         string   `json:"user_name"`
	UserID           string   `json:"user_id"`
	ARN              string   `json:"arn"`
	Path             string   `json:"path"`
	CreatedAt        string   `json:"created_at"`
	AccessKeys       []string `json:"access_keys"`
	Tags             []Tag    `json:"tags"`
	AttachedPolicies []string `json:"attached_policies"` // policy ARNs
}

// AccessKey represents an IAM access key stored in JetStream KV.
// SecretAccessKey is AES-256-GCM encrypted (base64-encoded), not hashed,
// so the SigV4 middleware can recover the plaintext for signature verification.
type AccessKey struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"` // AES-256-GCM encrypted, base64-encoded
	UserName        string `json:"user_name"`
	Status          string `json:"status"` // Active or Inactive
	CreatedAt       string `json:"created_at"`
}

// Tag represents a key-value tag on an IAM resource.
type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Policy represents a managed IAM policy stored in JetStream KV.
type Policy struct {
	PolicyName     string `json:"policy_name"`
	PolicyID       string `json:"policy_id"`
	ARN            string `json:"arn"`
	Path           string `json:"path"`
	Description    string `json:"description,omitempty"`
	PolicyDocument string `json:"policy_document"` // JSON string
	CreatedAt      string `json:"created_at"`
	DefaultVersion string `json:"default_version"` // always "v1"
	Tags           []Tag  `json:"tags"`
}

// PolicyDocument is the parsed IAM policy JSON structure.
type PolicyDocument struct {
	Version   string      `json:"Version"`
	Statement []Statement `json:"Statement"`
}

// Statement is a single statement within a policy document.
type Statement struct {
	Sid      string      `json:"Sid,omitempty"`
	Effect   string      `json:"Effect"`
	Action   StringOrArr `json:"Action"`
	Resource StringOrArr `json:"Resource"`
}

// StringOrArr handles JSON fields that can be either a string or an array of strings.
type StringOrArr []string

// UnmarshalJSON implements custom unmarshaling for string-or-array fields.
func (s *StringOrArr) UnmarshalJSON(data []byte) error {
	// Try string first
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = []string{single}
		return nil
	}

	// Try array
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	*s = arr
	return nil
}

// MarshalJSON marshals as a string if single element, otherwise as an array.
func (s StringOrArr) MarshalJSON() ([]byte, error) {
	if len(s) == 1 {
		return json.Marshal(s[0])
	}
	return json.Marshal([]string(s))
}

// BootstrapData is the on-disk JSON file consumed on first gateway start
// to seed the root IAM user into NATS KV.
type BootstrapData struct {
	AccessKeyID     string `json:"access_key_id"`
	EncryptedSecret string `json:"encrypted_secret"`
	AccountID       string `json:"account_id"`
}
