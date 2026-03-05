package utils

import "github.com/nats-io/nats.go"

// GlobalAccountID is the root/system account. Pre-Phase4 resources are owned by this account.
const GlobalAccountID = "000000000000"

// AccountKey returns a KV key scoped to an account: "{accountID}.{resourceID}".
func AccountKey(accountID, resourceID string) string {
	return accountID + "." + resourceID
}

// IsAccountID checks if a string is a valid 12-digit AWS account ID.
func IsAccountID(s string) bool {
	if len(s) != 12 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// GetOrCreateKVBucket creates or retrieves a NATS KV bucket.
func GetOrCreateKVBucket(js nats.JetStreamContext, bucketName string, history int) (nats.KeyValue, error) {
	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket:  bucketName,
		History: SafeIntToUint8(history),
	})
	if err != nil {
		kv, err = js.KeyValue(bucketName)
		if err != nil {
			return nil, err
		}
	}
	return kv, nil
}
