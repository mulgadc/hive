package handlers_iam

// User represents an IAM user stored in JetStream KV.
type User struct {
	UserName   string   `json:"user_name"`
	UserID     string   `json:"user_id"`
	ARN        string   `json:"arn"`
	Path       string   `json:"path"`
	CreatedAt  string   `json:"created_at"`
	AccessKeys []string `json:"access_keys"`
	Tags       []Tag    `json:"tags"`
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

// BootstrapData is the on-disk JSON file consumed on first gateway start
// to seed the root IAM user into NATS KV.
type BootstrapData struct {
	AccessKeyID     string `json:"access_key_id"`
	EncryptedSecret string `json:"encrypted_secret"`
	AccountID       string `json:"account_id"`
}
