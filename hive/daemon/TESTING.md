# Daemon Testing Guide

## Overview

The `daemon_test.go` file provides comprehensive unit tests for the daemon's EC2 launch functionality, focusing on message handling, validation, and resource management.

## Test Structure

### Core Test Components

1. **NATS Server Setup** (`startTestNATSServer`)
   - Creates an embedded NATS server for testing
   - Uses port `-1` for automatic port allocation
   - Returns server instance and connection URL
   - Automatically starts and waits for readiness

2. **Daemon Initialization** (`createTestDaemon`)
   - Creates a minimal daemon configuration for testing
   - Sets up temporary directories for test data
   - Establishes NATS connection
   - Includes automatic cleanup via `t.Cleanup()`

3. **Test Fixtures** (`createValidRunInstancesInput`)
   - Provides valid `ec2.RunInstancesInput` structures
   - Pre-configured with sensible defaults
   - Easy to modify for specific test scenarios

### Test Suites

#### 1. TestHandleEC2Launch_MessageParsing
Tests the daemon's ability to parse and validate incoming NATS messages:

- ✅ Valid RunInstancesInput processing
- ✅ Invalid instance type handling
- ✅ Missing required fields (ImageId)
- ✅ Invalid parameter values (MinCount = 0)
- ✅ Malformed JSON handling

**Expected Behaviors:**
- Valid inputs should return a Reservation with pending instance
- Invalid inputs should return error payloads (not transport errors)
- All responses should be properly formatted JSON

#### 2. TestHandleEC2Launch_ResourceManagement
Tests resource allocation and instance type validation:

- ✅ Valid instance types (t3.micro, t3.nano)
- ✅ Invalid instance types (t99.invalid)
- ✅ Resource availability checking
- ✅ Allocation/deallocation cycles

#### 3. TestDaemon_Initialization
Tests daemon initialization logic:

- ✅ Configuration loading
- ✅ Resource manager creation
- ✅ Instance storage initialization

#### 4. TestResourceManager
Tests the resource management subsystem:

- ✅ System resource detection
- ✅ Instance type allocation
- ✅ Resource deallocation
- ✅ Capacity validation

## Running Tests

### Basic Test Execution

```bash
# Run all daemon tests
go test -v ./hive/daemon/

# Run specific test
go test -v ./hive/daemon/ -run TestHandleEC2Launch_MessageParsing

# Run with race detection
go test -race -v ./hive/daemon/

# Run with coverage
go test -cover -v ./hive/daemon/
```

### Skipping Integration Tests

The tests are designed to skip integration components when running on macOS or when dependencies are unavailable:

```bash
# Skip integration tests that require viperblock/nbdkit
SKIP_INTEGRATION=1 go test -v ./hive/daemon/
```

## macOS Testing Limitations

### Current Limitations

The full integration test suite has limitations on macOS due to missing dependencies:

1. **Viperblock** - Not yet ported to macOS
   - NBD (Network Block Device) support not available
   - Volume mounting operations will fail

2. **NBDkit** - Linux-specific tool
   - Requires kernel NBD support
   - Not available natively on macOS

3. **QEMU/KVM** - Limited on macOS
   - KVM acceleration not available (Linux-only)
   - Can use QEMU with TCG (slow emulation)

### What Works on macOS

✅ **Message-driven architecture testing:**
- NATS message parsing and routing
- Request/response validation
- Error handling and payload generation
- Resource manager logic

✅ **Unit tests without infrastructure:**
- Input validation (`ValidateRunInstancesInput`)
- JSON marshaling/unmarshaling
- Error code generation
- Instance ID generation

### What Requires Linux

❌ **Full integration testing:**
- Volume creation and mounting (requires viperblock)
- NBD device attachment (requires nbdkit)
- VM launch with KVM acceleration
- EBS volume operations
- Cloud-init ISO creation and mounting

### Recommendations for macOS Development

#### Option 1: Mock Integration Points

Create mock implementations for macOS testing:

```go
// daemon_test_helpers.go
// +build darwin

// Mock viperblock operations for macOS
type MockViperblock struct {
    // Mock implementation
}

func (m *MockViperblock) Mount(volumeId string) error {
    // Return mock NBD URI
    return nil
}
```

#### Option 2: Use Docker Linux Container

Run tests in a Linux container with full dependencies:

```bash
# Build Linux test container
docker build -t hive-test -f Dockerfile.test .

# Run tests in container
docker run --rm \
  --privileged \
  -v $(pwd):/workspace \
  hive-test \
  go test -v ./hive/daemon/
```

**Dockerfile.test example:**
```dockerfile
FROM ubuntu:22.04

RUN apt-get update && apt-get install -y \
    golang-1.21 \
    qemu-kvm \
    nbdkit \
    build-essential

WORKDIR /workspace
```

#### Option 3: GitHub Actions / CI Pipeline

Use CI for full integration tests:

```yaml
# .github/workflows/test.yml
name: Integration Tests
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
      - name: Install dependencies
        run: |
          sudo apt-get update
          sudo apt-get install -y nbdkit qemu-kvm
      - name: Run tests
        run: make test
```

#### Option 4: Virtual Linux VM on macOS

Use a Linux VM for development testing:

```bash
# Using UTM or Multipass
multipass launch --name hive-dev --cpus 4 --memory 8G --disk 20G
multipass shell hive-dev

# Inside VM
cd /path/to/hive
make test
```

### Hybrid Testing Strategy

**On macOS (Unit Tests):**
```bash
# Test message handling, validation, resource management
SKIP_INTEGRATION=1 go test -v ./hive/daemon/
```

**On Linux (Full Integration):**
```bash
# Test complete EC2 launch flow with viperblock/nbdkit
go test -v ./hive/daemon/
```

## Test Coverage Goals

### Current Coverage
- ✅ Message parsing and validation
- ✅ Resource allocation logic
- ✅ Error handling and responses
- ✅ NATS integration (pub/sub)

### Additional Tests Needed (Linux)

#### Volume Management Tests
```go
func TestHandleEC2Launch_VolumeCreation(t *testing.T) {
    // Test AMI volume cloning
    // Test EFI partition creation
    // Test cloud-init ISO generation
}
```

#### NBD Mount Tests
```go
func TestMountVolumes(t *testing.T) {
    // Test NBD URI generation
    // Test mount request/response
    // Test mount failures
}
```

#### VM Lifecycle Tests
```go
func TestLaunchInstance(t *testing.T) {
    // Test QEMU process startup
    // Test QMP socket connection
    // Test instance state transitions
}
```

#### End-to-End Tests
```go
func TestEC2Launch_EndToEnd(t *testing.T) {
    // Full flow: message -> volumes -> VM -> running
    // Test with multiple instance types
    // Test concurrent launches
}
```

## Mocking Strategy for macOS

### Mock EBS Mount Service

```go
// Create a mock EBS mount responder
func startMockEBSService(t *testing.T, nc *nats.Conn) {
    nc.QueueSubscribe("ebs.mount", "mock-ebs", func(msg *nats.Msg) {
        var req config.EBSRequest
        json.Unmarshal(msg.Data, &req)

        resp := config.EBSMountResponse{
            URI:     fmt.Sprintf("nbd://localhost:10809/%s", req.Name),
            Mounted: true,
            Error:   "",
        }

        data, _ := json.Marshal(resp)
        msg.Respond(data)
    })
}
```

### Mock S3 Client

```go
// Mock S3 client for SSH key retrieval
type MockS3Client struct {
    keys map[string]string
}

func (m *MockS3Client) Read(path string) ([]byte, error) {
    if key, ok := m.keys[path]; ok {
        return []byte(key), nil
    }
    return nil, fmt.Errorf("key not found")
}
```

### Example Mock Test

```go
func TestHandleEC2Launch_WithMocks(t *testing.T) {
    if runtime.GOOS == "darwin" {
        // Run with mocks on macOS
        ns, natsURL := startTestNATSServer(t)
        defer ns.Shutdown()

        nc, _ := nats.Connect(natsURL)
        defer nc.Close()

        // Start mock services
        startMockEBSService(t, nc)
        startMockS3Service(t, nc)

        // Run test...
    } else {
        // Run with real services on Linux
        // ...
    }
}
```

## Debugging Tests

### Enable Verbose Logging

```bash
# Run tests with verbose output
go test -v ./hive/daemon/ 2>&1 | tee test.log
```

### Debug NATS Messages

```go
// Add debug subscriber to see all messages
nc.Subscribe(">", func(msg *nats.Msg) {
    t.Logf("NATS [%s]: %s", msg.Subject, string(msg.Data))
})
```

### Common Issues

**Issue: "NATS server failed to start"**
- Port already in use
- Use `-1` for auto port allocation
- Check firewall settings

**Issue: "Request timeout"**
- Increase timeout duration
- Check NATS connectivity
- Verify handler subscription

**Issue: "Test fails on Linux but works on macOS"**
- Missing dependencies (nbdkit, QEMU)
- Permission issues (KVM access)
- File system differences

## Next Steps

1. ✅ Create basic message handling tests (DONE)
2. ⏳ Add mock services for macOS testing
3. ⏳ Create Linux CI pipeline for full integration tests
4. ⏳ Add Docker-based test environment
5. ⏳ Implement volume lifecycle tests (Linux only)
6. ⏳ Add concurrent launch stress tests
7. ⏳ Create performance benchmarks

## Contributing

When adding new tests:

1. **Check platform compatibility:**
   ```go
   if runtime.GOOS == "darwin" && needsLinux {
       t.Skip("Test requires Linux")
   }
   ```

2. **Use table-driven tests:**
   ```go
   tests := []struct {
       name string
       input interface{}
       want interface{}
   }{ /* ... */ }
   ```

3. **Clean up resources:**
   ```go
   t.Cleanup(func() {
       // Cleanup code
   })
   ```

4. **Document dependencies:**
   - Add comments explaining Linux-only requirements
   - Use build tags where appropriate
   - Provide alternative test paths for macOS
