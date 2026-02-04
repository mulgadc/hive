# MulgaOS Hive Security Guide

This document outlines security recommendations, best practices, and implementation guidelines for MulgaOS Hive.

## Top 20 Security Recommendations

### 1. Rate Limiting - API Gateway (Critical)

**Current Status**: ❌ Not Implemented

**Recommendation**: Implement rate limiting at the AWS Gateway (`awsd`) level.

```go
// Suggested implementation in gateway/ec2.go
type RateLimiter struct {
    requests    map[string]*RateWindow  // IP -> rate window
    maxRequests int                      // e.g., 1000/minute
    windowSize  time.Duration            // e.g., 1 minute
    mu          sync.RWMutex
}

func (rl *RateLimiter) Allow(ip string) bool {
    // Check if IP exceeded rate limit
}
```

**Settings**:
- Global max connections: 10,000 (configurable)
- Per-IP rate limit: 100 requests/second for external, 1000/second for local
- Per-access-key rate limit: 500 requests/second
- Burst allowance: 2x normal rate for 10 seconds

### 2. Rate Limiting - Per Tenant (High)

**Current Status**: ❌ Not Implemented

**Recommendation**: Track requests per AWS access key to prevent single-tenant abuse.

```go
type TenantRateLimiter struct {
    accessKeyLimits map[string]*RateBucket
    maxPerTenant    int64  // Max requests per minute per tenant
}
```

**Benefits**:
- Prevents single tenant from exhausting resources
- Fair resource distribution
- DoS protection

### 3. Input Validation (Critical)

**Current Status**: ⚠️ Partial

**Recommendation**: Add strict validation for all user inputs.

**Validation Points**:
- VPC CIDR blocks (valid IPv4/IPv6 ranges)
- Instance types (whitelist only valid types)
- AMI IDs (regex: `^ami-[a-f0-9]{8,17}$`)
- Security group rules (valid port ranges 0-65535)
- Tag keys/values (length limits, character restrictions)

```go
// Example validation helper
func ValidateAMIID(amiID string) error {
    matched, _ := regexp.MatchString(`^ami-[a-f0-9]{8,17}$`, amiID)
    if !matched {
        return errors.New("InvalidAMIID.Malformed")
    }
    return nil
}
```

### 4. Authentication Token Security (Critical)

**Current Status**: ✅ Implemented (basic)

**Recommendations**:
- Store access key secrets hashed (SHA-256) - ✅ Done
- Implement token expiration for IAM session tokens
- Add MFA support for sensitive operations
- Rotate NATS tokens periodically

### 5. TLS/SSL Configuration (High)

**Current Status**: ✅ Implemented

**Recommendations**:
- Minimum TLS 1.2 (preferably 1.3)
- Strong cipher suites only
- Certificate rotation automation
- HSTS headers for web UI

```go
// Recommended TLS config
tlsConfig := &tls.Config{
    MinVersion:               tls.VersionTLS12,
    PreferServerCipherSuites: true,
    CipherSuites: []uint16{
        tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
        tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
        tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
    },
}
```

### 6. NATS JetStream KV Tenant Isolation (Critical)

**Current Status**: ⚠️ Partial

**Recommendation**: Enforce tenant ID prefix on all KV operations.

```go
// All KV keys should include tenant ID
func (s *VPCServiceImpl) getAllVPCs(tenantID string) ([]VPCRecord, error) {
    prefix := fmt.Sprintf("tenant-%s-", tenantID)
    keys, err := s.vpcsKV.Keys()

    // Filter keys by tenant prefix
    for _, key := range keys {
        if strings.HasPrefix(key, prefix) {
            // Process...
        }
    }
}
```

**Implementation Steps**:
1. Add `tenantID` to all request contexts
2. Prefix all KV keys with tenant ID
3. Add unit tests for cross-tenant access attempts

### 7. Go Fuzzing for Critical Functions (Medium)

**Current Status**: ❌ Not Implemented

**Recommendation**: Add fuzz tests for input parsers.

```go
// Example fuzz test for EC2 query parser
func FuzzParseEC2Query(f *testing.F) {
    f.Add("Action=RunInstances&ImageId=ami-12345678")
    f.Add("")
    f.Add("Action=<script>alert('xss')</script>")

    f.Fuzz(func(t *testing.T, input string) {
        _, _ = ParseEC2Query(input)
        // Should not panic
    })
}
```

**Priority Functions to Fuzz**:
- `awsec2query.ParseQuery()`
- `utils.UnmarshalJsonPayload()`
- VPC CIDR parsing
- Security group rule parsing

### 8. S3 Bucket Encryption at Rest (High)

**Current Status**: ❌ Not Implemented

**Recommendation**: Implement AES-256 encryption for S3 objects.

**Design Options**:
1. **Server-Side Encryption (SSE-S3)**: Managed encryption keys
2. **SSE-KMS**: Customer-managed keys via KMS service
3. **Client-Side Encryption**: Application encrypts before upload

**Suggested Implementation**:
```go
type EncryptedBucket struct {
    bucket    *predastore.Bucket
    masterKey []byte  // From KMS or secure vault
}

func (eb *EncryptedBucket) PutObject(key string, data []byte) error {
    // Generate per-object key
    objectKey := deriveKey(eb.masterKey, key)

    // Encrypt with AES-256-GCM
    encrypted, err := encryptAESGCM(data, objectKey)
    if err != nil {
        return err
    }

    return eb.bucket.Put(key, encrypted)
}
```

### 9. Volume Encryption (EBS) (High)

**Current Status**: ❌ Not Implemented

**Recommendation**: Support encrypted EBS volumes using LUKS or dm-crypt.

```go
type EncryptedVolume struct {
    volume     *viperblock.Volume
    keyID      string  // Reference to encryption key
    algorithm  string  // e.g., "aes-xts-plain64"
}
```

### 10. Audit Logging (Critical)

**Current Status**: ⚠️ Basic slog

**Recommendation**: Implement structured audit logging.

```go
type AuditEvent struct {
    Timestamp     time.Time         `json:"timestamp"`
    EventType     string            `json:"event_type"`
    TenantID      string            `json:"tenant_id"`
    UserID        string            `json:"user_id"`
    AccessKeyID   string            `json:"access_key_id"`
    Action        string            `json:"action"`
    Resource      string            `json:"resource"`
    SourceIP      string            `json:"source_ip"`
    Success       bool              `json:"success"`
    ErrorMessage  string            `json:"error_message,omitempty"`
    RequestParams map[string]string `json:"request_params,omitempty"`
}
```

**Events to Log**:
- All IAM operations (user/key/policy CRUD)
- Instance launches/terminations
- Security group modifications
- VPC changes
- Authentication failures

### 11. Network Segmentation (Medium)

**Current Status**: ✅ Implemented (OVS VLANs)

**Recommendations**:
- Verify VLAN isolation between tenants
- Add OpenFlow rules for explicit deny by default
- Implement network policies per VPC

### 12. QEMU Security Hardening (High)

**Current Status**: ⚠️ Partial

**Recommendations**:
```bash
# Recommended QEMU security flags
-sandbox on,obsolete=deny,elevateprivileges=deny,spawn=deny,resourcecontrol=deny
-no-user-config
-nodefaults
```

**Additional Hardening**:
- Run QEMU as unprivileged user
- Use seccomp filtering
- Enable SELinux/AppArmor profiles

### 13. Metadata Server Security (High)

**Current Status**: ✅ Implemented (IMDSv2)

**Recommendations**:
- ✅ IMDSv2 token required (implemented)
- Limit token TTL (max 6 hours)
- Log metadata access
- Consider IP-based restrictions

### 14. Connection Timeout Configuration (Medium)

**Current Status**: ⚠️ Partial

**Recommendation**: Configure timeouts at all layers.

```go
// HTTP server timeouts
server := &http.Server{
    ReadTimeout:       10 * time.Second,
    WriteTimeout:      30 * time.Second,
    IdleTimeout:       120 * time.Second,
    ReadHeaderTimeout: 5 * time.Second,
}

// NATS timeouts
opts := []nats.Option{
    nats.Timeout(10 * time.Second),
    nats.DrainTimeout(30 * time.Second),
    nats.PingInterval(20 * time.Second),
    nats.MaxPingsOutstanding(2),
}
```

### 15. Resource Quotas (High)

**Current Status**: ❌ Not Implemented

**Recommendation**: Implement per-tenant resource quotas.

```go
type TenantQuotas struct {
    MaxInstances        int
    MaxVPCs            int
    MaxSecurityGroups   int
    MaxVolumes         int
    MaxVolumeStorageGB int64
    MaxSnapshots       int
}

func (q *TenantQuotas) CanLaunchInstance(tenant string) (bool, error) {
    current := countTenantInstances(tenant)
    return current < q.MaxInstances, nil
}
```

### 16. Secrets Management (High)

**Current Status**: ⚠️ File-based

**Recommendations**:
- Store secrets in encrypted KV store
- Implement secret rotation
- Use environment variables over config files
- Consider HashiCorp Vault integration

### 17. API Request Signing Validation (Medium)

**Current Status**: ⚠️ Basic

**Recommendation**: Strict AWS Signature Version 4 validation.

```go
func ValidateAWSSigV4(r *http.Request) error {
    // Verify timestamp within 15 minutes
    // Validate signature components
    // Check for replay attacks (nonce tracking)
}
```

### 18. Error Message Sanitization (Medium)

**Current Status**: ⚠️ Partial

**Recommendation**: Don't leak internal details in error messages.

```go
// Bad: Exposes internal paths
return fmt.Errorf("failed to read /opt/hive/data/volumes/%s: permission denied", volumeID)

// Good: Generic error with correlation ID
return fmt.Errorf("InternalError: Failed to access volume. Request ID: %s", requestID)
```

### 19. Dependency Security (Medium)

**Current Status**: ⚠️ Manual

**Recommendations**:
- Run `govulncheck` in CI/CD
- Pin dependency versions in go.mod
- Regular security audits
- Use `go mod verify`

```bash
# Add to CI pipeline
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
```

### 20. Instance Metadata Access Control (Medium)

**Current Status**: ✅ Per-instance isolation

**Additional Recommendations**:
- Track metadata access patterns
- Detect anomalous access (potential SSRF)
- Consider IAM role credential rate limiting

---

## Implementation Priority

### Phase 1 - Critical (Immediate)
1. Rate limiting at API Gateway
2. Tenant isolation in NATS KV
3. Input validation improvements
4. Audit logging

### Phase 2 - High (Within 30 days)
5. S3/EBS encryption at rest
6. Resource quotas
7. QEMU security hardening
8. Per-tenant rate limiting

### Phase 3 - Medium (Within 90 days)
9. Go fuzzing implementation
10. Connection timeout tuning
11. Secrets management improvements
12. Dependency security scanning

---

## Security Testing Checklist

- [ ] Rate limit bypass attempts
- [ ] Cross-tenant data access
- [ ] SQL/NoSQL injection in KV queries
- [ ] XSS in metadata/tags
- [ ] Path traversal in volume/image paths
- [ ] SSRF via metadata service
- [ ] Authentication bypass
- [ ] Privilege escalation
- [ ] DoS resistance
- [ ] Replay attack prevention

---

## Incident Response

### Suspected Compromise

1. Rotate all access keys for affected tenant
2. Revoke active sessions
3. Review audit logs
4. Terminate suspicious instances
5. Reset NATS tokens if cluster-wide compromise

### Security Contact

Report security vulnerabilities to: security@mulgadc.com

---

## Compliance Notes

While MulgaOS is designed for self-hosted environments, these security controls align with:
- AWS Well-Architected Framework (Security Pillar)
- CIS Benchmarks for cloud infrastructure
- OWASP API Security Top 10

---

*Last Updated: February 2026*
