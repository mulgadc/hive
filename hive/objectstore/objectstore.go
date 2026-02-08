// Package objectstore provides a common abstraction for S3-like object storage operations.
// This package enables handlers to work with either real S3 backends (Predastore) or
// in-memory stores for testing, without changing the handler implementation.
package objectstore

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"golang.org/x/net/http2"
)

// NoSuchKeyError represents a missing object error, compatible with AWS S3 errors
type NoSuchKeyError struct {
	Key string
}

func (e *NoSuchKeyError) Error() string {
	return "NoSuchKey: " + e.Key
}

func (e *NoSuchKeyError) Code() string {
	return "NoSuchKey"
}

// IsNoSuchKeyError checks if an error is a NoSuchKey error
func IsNoSuchKeyError(err error) bool {
	var noSuchKey *NoSuchKeyError
	return errors.As(err, &noSuchKey)
}

// ObjectStore defines the interface for S3-like object storage operations.
// This abstraction allows for mocking in tests without requiring actual S3 connectivity.
type ObjectStore interface {
	// GetObject retrieves an object from storage
	GetObject(input *s3.GetObjectInput) (*s3.GetObjectOutput, error)

	// PutObject stores an object in storage
	PutObject(input *s3.PutObjectInput) (*s3.PutObjectOutput, error)

	// DeleteObject removes an object from storage
	DeleteObject(input *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error)

	// ListObjects lists objects in a bucket
	ListObjects(input *s3.ListObjectsInput) (*s3.ListObjectsOutput, error)
}

// NewS3ObjectStoreFromConfig creates an S3ObjectStore from Predastore connection parameters,
// eliminating the duplicated TLS+HTTP/2+session boilerplate in each service.
func NewS3ObjectStoreFromConfig(host, region, accessKey, secretKey string) *S3ObjectStore {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // Skip TLS verification for self-signed certs
			NextProtos:         []string{"h2", "http/1.1"},
		},
		ForceAttemptHTTP2: true,
	}

	if err := http2.ConfigureTransport(tr); err != nil {
		slog.Warn("Failed to configure HTTP/2", "error", err)
	}

	httpClient := &http.Client{Transport: tr}

	sess := session.Must(session.NewSession(&aws.Config{
		Endpoint:         aws.String(host),
		Region:           aws.String(region),
		Credentials:      credentials.NewStaticCredentials(accessKey, secretKey, ""),
		S3ForcePathStyle: aws.Bool(true),
		HTTPClient:       httpClient,
	}))

	return NewS3ObjectStore(s3.New(sess))
}

// S3ObjectStore wraps the AWS S3 client to implement ObjectStore
type S3ObjectStore struct {
	client *s3.S3
}

// NewS3ObjectStore creates an ObjectStore backed by AWS S3 or S3-compatible storage
func NewS3ObjectStore(client *s3.S3) *S3ObjectStore {
	return &S3ObjectStore{client: client}
}

func (s *S3ObjectStore) GetObject(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
	out, err := s.client.GetObject(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok &&
			(aerr.Code() == s3.ErrCodeNoSuchKey || aerr.Code() == "NotFound") {
			return nil, &NoSuchKeyError{Key: aws.StringValue(input.Key)}
		}
		return nil, err
	}
	return out, nil
}

func (s *S3ObjectStore) PutObject(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
	return s.client.PutObject(input)
}

func (s *S3ObjectStore) DeleteObject(input *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error) {
	return s.client.DeleteObject(input)
}

func (s *S3ObjectStore) ListObjects(input *s3.ListObjectsInput) (*s3.ListObjectsOutput, error) {
	return s.client.ListObjects(input)
}

// MemoryObjectStore implements ObjectStore using in-memory storage for testing
type MemoryObjectStore struct {
	objects map[string][]byte // key: bucket/key -> value: object data
	mu      sync.RWMutex
}

// NewMemoryObjectStore creates an in-memory ObjectStore for testing
func NewMemoryObjectStore() *MemoryObjectStore {
	return &MemoryObjectStore{
		objects: make(map[string][]byte),
	}
}

// makeKey creates a storage key from bucket and key
func makeKey(bucket, key string) string {
	return bucket + "/" + key
}

func (m *MemoryObjectStore) GetObject(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	storageKey := makeKey(*input.Bucket, *input.Key)
	data, exists := m.objects[storageKey]
	if !exists {
		return nil, &NoSuchKeyError{Key: *input.Key}
	}

	return &s3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader(data)),
		ContentLength: aws.Int64(int64(len(data))),
	}, nil
}

func (m *MemoryObjectStore) PutObject(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	storageKey := makeKey(*input.Bucket, *input.Key)

	data, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}

	m.objects[storageKey] = data

	return &s3.PutObjectOutput{}, nil
}

func (m *MemoryObjectStore) DeleteObject(input *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	storageKey := makeKey(*input.Bucket, *input.Key)
	delete(m.objects, storageKey)

	return &s3.DeleteObjectOutput{}, nil
}

func (m *MemoryObjectStore) ListObjects(input *s3.ListObjectsInput) (*s3.ListObjectsOutput, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bucket := *input.Bucket
	prefix := ""
	if input.Prefix != nil {
		prefix = *input.Prefix
	}
	delimiter := ""
	if input.Delimiter != nil {
		delimiter = *input.Delimiter
	}

	var contents []*s3.Object
	prefixes := make(map[string]bool)

	for key, data := range m.objects {
		// Check if key belongs to this bucket
		if !strings.HasPrefix(key, bucket+"/") {
			continue
		}

		// Extract the key part (remove bucket/)
		objectKey := key[len(bucket)+1:]

		// Check prefix filter
		if prefix != "" && !strings.HasPrefix(objectKey, prefix) {
			continue
		}

		// Handle delimiter (common prefixes)
		if delimiter != "" {
			// Find the position after the prefix where delimiter appears
			afterPrefix := objectKey[len(prefix):]
			if idx := strings.Index(afterPrefix, delimiter); idx >= 0 {
				// This object is in a "subdirectory", add to common prefixes
				commonPrefix := objectKey[:len(prefix)+idx+len(delimiter)]
				prefixes[commonPrefix] = true
				continue
			}
		}

		contents = append(contents, &s3.Object{
			Key:  aws.String(objectKey),
			Size: aws.Int64(int64(len(data))),
		})
	}

	// Convert prefixes map to CommonPrefixes list
	var commonPrefixes []*s3.CommonPrefix
	for p := range prefixes {
		commonPrefixes = append(commonPrefixes, &s3.CommonPrefix{
			Prefix: aws.String(p),
		})
	}

	return &s3.ListObjectsOutput{
		Contents:       contents,
		CommonPrefixes: commonPrefixes,
		Name:           input.Bucket,
	}, nil
}

// Clear removes all objects from the memory store (useful for test cleanup)
func (m *MemoryObjectStore) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects = make(map[string][]byte)
}

// Count returns the number of objects in the memory store
func (m *MemoryObjectStore) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.objects)
}
