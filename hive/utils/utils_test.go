package utils

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateResourceID(t *testing.T) {
	tests := []struct {
		prefix string
	}{
		{"i"},
		{"r"},
		{"vol"},
		{"snap"},
		{"key"},
		{"eigw"},
		{"ami"},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			id := GenerateResourceID(tt.prefix)
			assert.True(t, strings.HasPrefix(id, tt.prefix+"-"))
			// prefix + "-" + 17 hex chars
			assert.Len(t, id, len(tt.prefix)+1+17)

			// Verify uniqueness
			id2 := GenerateResourceID(tt.prefix)
			assert.NotEqual(t, id, id2)
		})
	}
}

func TestGeneratePidFile(t *testing.T) {

	// Simulate a sample process running (e.g cat)
	cmd := exec.Command("cat")
	cmd.Start()

	err := WritePidFile("utilsunittest", cmd.Process.Pid)

	assert.NoError(t, err)

	// Read the PID file and verify contents
	pid, err := ReadPidFile("utilsunittest")

	assert.NoError(t, err)
	assert.Equal(t, cmd.Process.Pid, pid)

	// Test attempt to read a PID file that doesn't exist
	_, err = ReadPidFile("nonexistentpidfile")
	assert.Error(t, err)

	// Cleanup
	err = RemovePidFile("utilsunittest")
	assert.NoError(t, err)

	// Give some time before killing the process
	//time.Sleep(2 * time.Second)

	// Simulate process ending

}

func TestGenerateSocketFile(t *testing.T) {

	socketPath := fmt.Sprintf("%s/%s", os.TempDir(), "utilsunittest")

	name, err := GenerateSocketFile(socketPath)

	assert.NoError(t, err)

	assert.True(t, strings.HasSuffix(name, "utilsunittest.sock"))

	// Test empty socket path
	_, err = GenerateSocketFile("")

	assert.Error(t, err)

}

func TestExecProcessAndKill(t *testing.T) {

	// Simulate a sample process running (e.g sleep, 30 secs)
	cmd := exec.Command("sleep", "30")

	// Detach: new process group, no controlling terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // put child in new process group
	}

	// Make it fully background-friendly:
	// - close stdio so parent doesn't block on pipes
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	// Start (non-blocking). If command is missing, error.
	if err := cmd.Start(); err != nil {
		assert.Fail(t, "Failed to start command", err)
	}

	// IMPORTANT: reap the child to avoid zombies.
	// Since we're "backgrounding" it, do Wait() in a goroutine.
	go func(c *exec.Cmd) {
		t.Log("Waiting for command to finish...")
		_ = c.Wait() // ignore error; ensures kernel reaps the process
		t.Log("Command finished.")
	}(cmd)

	err := WritePidFile("utilsunittest", cmd.Process.Pid)

	t.Log("Started process with PID:", cmd.Process.Pid)

	assert.NoError(t, err)

	// Test PID file removed
	err = WaitForPidFileRemoval("utilsunittest", 100*time.Millisecond)
	assert.Error(t, err) // Should timeout since file should still exist

	time.Sleep(500 * time.Millisecond)

	// Kill the process
	err = StopProcess("utilsunittest")
	assert.NoError(t, err)

	// Test PID file removed
	err = WaitForPidFileRemoval("utilsunittest", 1*time.Second)
	assert.NoError(t, err) // Should timeout since file should still exist

	// Verify process is killed
	err = cmd.Process.Signal(syscall.Signal(0))
	assert.Error(t, err) // Should return an error since process is killed

}

func TestUnmarshalJsonPayload(t *testing.T) {
	type TestStruct struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	tests := []struct {
		name        string
		jsonData    string
		expectError bool
		validate    func(t *testing.T, result *TestStruct)
	}{
		{
			name:        "Valid JSON",
			jsonData:    `{"name":"test","value":123}`,
			expectError: false,
			validate: func(t *testing.T, result *TestStruct) {
				assert.Equal(t, "test", result.Name)
				assert.Equal(t, 123, result.Value)
			},
		},
		{
			name:        "Invalid JSON - malformed",
			jsonData:    `{"name":"test","value":}`,
			expectError: true,
			validate:    nil,
		},
		{
			name:        "Invalid JSON - unknown field",
			jsonData:    `{"name":"test","value":123,"unknown":"field"}`,
			expectError: true, // DisallowUnknownFields should cause error
			validate:    nil,
		},
		{
			name:        "Empty JSON",
			jsonData:    `{}`,
			expectError: false,
			validate: func(t *testing.T, result *TestStruct) {
				assert.Equal(t, "", result.Name)
				assert.Equal(t, 0, result.Value)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result TestStruct
			errResp := UnmarshalJsonPayload(&result, []byte(tt.jsonData))

			if tt.expectError {
				assert.NotNil(t, errResp, "Expected error response")
			} else {
				assert.Nil(t, errResp, "Expected no error response")
				if tt.validate != nil {
					tt.validate(t, &result)
				}
			}
		})
	}
}

func TestMarshalJsonPayload(t *testing.T) {
	type TestStruct struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	tests := []struct {
		name        string
		jsonData    string
		expectError bool
		validate    func(t *testing.T, result *TestStruct)
	}{
		{
			name:        "Valid JSON",
			jsonData:    `{"name":"test","value":456}`,
			expectError: false,
			validate: func(t *testing.T, result *TestStruct) {
				assert.Equal(t, "test", result.Name)
				assert.Equal(t, 456, result.Value)
			},
		},
		{
			name:        "Invalid JSON",
			jsonData:    `{"name":"test","value":}`,
			expectError: true,
			validate:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result TestStruct
			errResp := MarshalJsonPayload(&result, []byte(tt.jsonData))

			if tt.expectError {
				assert.NotNil(t, errResp, "Expected error response")
			} else {
				assert.Nil(t, errResp, "Expected no error response")
				if tt.validate != nil {
					tt.validate(t, &result)
				}
			}
		})
	}
}

func TestGenerateErrorPayload(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		validate func(t *testing.T, payload []byte)
	}{
		{
			name: "ValidationError",
			code: "ValidationError",
			validate: func(t *testing.T, payload []byte) {
				assert.Contains(t, string(payload), "ValidationError")
				assert.Contains(t, string(payload), "Code")
			},
		},
		{
			name: "InvalidInstanceType",
			code: "InvalidInstanceType",
			validate: func(t *testing.T, payload []byte) {
				assert.Contains(t, string(payload), "InvalidInstanceType")
			},
		},
		{
			name: "CustomError",
			code: "CustomError",
			validate: func(t *testing.T, payload []byte) {
				assert.Contains(t, string(payload), "CustomError")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := GenerateErrorPayload(tt.code)
			assert.NotNil(t, payload)
			assert.Greater(t, len(payload), 0)
			if tt.validate != nil {
				tt.validate(t, payload)
			}
		})
	}
}

func TestValidateErrorPayload(t *testing.T) {
	tests := []struct {
		name         string
		payload      string
		expectError  bool
		expectedCode string
	}{
		{
			name:         "Valid error payload",
			payload:      `{"Code":"ValidationError","Message":null}`,
			expectError:  true,
			expectedCode: "ValidationError",
		},
		{
			name:        "Valid success payload (no Code field)",
			payload:     `{"ReservationId":"r-123","Instances":[]}`,
			expectError: false,
		},
		{
			name:        "Empty payload",
			payload:     `{}`,
			expectError: true, // Empty payload treated as error by ValidateErrorPayload
		},
		{
			name:        "Invalid JSON",
			payload:     `{invalid}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responseError, err := ValidateErrorPayload([]byte(tt.payload))

			if tt.expectError {
				if tt.expectedCode != "" {
					// Check for specific error code
					assert.Error(t, err)
					if responseError.Code != nil {
						assert.Equal(t, tt.expectedCode, *responseError.Code)
					}
				}
			} else {
				// No error expected
				assert.NoError(t, err)
			}
		})
	}
}

func TestParseNBDURI(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		wantType string
		wantPath string
		wantHost string
		wantPort int
		wantErr  bool
	}{
		{
			name:     "Unix socket",
			uri:      "nbd:unix:/run/user/1000/nbd-vol-123.sock",
			wantType: "unix",
			wantPath: "/run/user/1000/nbd-vol-123.sock",
		},
		{
			name:     "TCP address",
			uri:      "nbd://127.0.0.1:34305",
			wantType: "inet",
			wantHost: "127.0.0.1",
			wantPort: 34305,
		},
		{
			name:     "TCP with hostname",
			uri:      "nbd://storage.local:9000",
			wantType: "inet",
			wantHost: "storage.local",
			wantPort: 9000,
		},
		{
			name:    "Empty socket path",
			uri:     "nbd:unix:",
			wantErr: true,
		},
		{
			name:    "Missing port in TCP",
			uri:     "nbd://127.0.0.1",
			wantErr: true,
		},
		{
			name:    "Invalid port",
			uri:     "nbd://127.0.0.1:notaport",
			wantErr: true,
		},
		{
			name:    "Unsupported format",
			uri:     "http://example.com",
			wantErr: true,
		},
		{
			name:    "Empty string",
			uri:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serverType, path, host, port, err := ParseNBDURI(tt.uri)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.wantType, serverType)
			assert.Equal(t, tt.wantPath, path)
			assert.Equal(t, tt.wantHost, host)
			assert.Equal(t, tt.wantPort, port)
		})
	}
}

func TestMarshalToXML(t *testing.T) {
	type TestStruct struct {
		Name  string `xml:"Name"`
		Value int    `xml:"Value"`
	}

	tests := []struct {
		name        string
		input       any
		expectError bool
		validate    func(t *testing.T, xmlData []byte)
	}{
		{
			name: "Valid struct",
			input: TestStruct{
				Name:  "test",
				Value: 123,
			},
			expectError: false,
			validate: func(t *testing.T, xmlData []byte) {
				assert.Contains(t, string(xmlData), "<Name>test</Name>")
				assert.Contains(t, string(xmlData), "<Value>123</Value>")
			},
		},
		{
			name: "Pointer to struct",
			input: &TestStruct{
				Name:  "pointer",
				Value: 456,
			},
			expectError: false,
			validate: func(t *testing.T, xmlData []byte) {
				assert.Contains(t, string(xmlData), "<Name>pointer</Name>")
				assert.Contains(t, string(xmlData), "<Value>456</Value>")
			},
		},
		{
			name:        "Invalid type (channel)",
			input:       make(chan int),
			expectError: true,
			validate:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xmlData, err := MarshalToXML(tt.input)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, xmlData)
				if tt.validate != nil {
					tt.validate(t, xmlData)
				}
			}
		})
	}
}

func TestGenerateIAMXMLPayload(t *testing.T) {
	type User struct {
		UserName string `locationName:"UserName" type:"string"`
		UserId   string `locationName:"UserId" type:"string"`
	}

	tests := []struct {
		name     string
		action   string
		payload  any
		validate func(t *testing.T, result any)
	}{
		{
			name:   "CreateUser wrapping",
			action: "CreateUser",
			payload: User{
				UserName: "testuser",
				UserId:   "AIDA12345",
			},
			validate: func(t *testing.T, result any) {
				xmlBytes, err := MarshalToXML(result)
				assert.NoError(t, err)
				xmlStr := string(xmlBytes)
				assert.Contains(t, xmlStr, "CreateUserResponse")
				assert.Contains(t, xmlStr, "CreateUserResult")
				assert.Contains(t, xmlStr, "testuser")
				assert.Contains(t, xmlStr, "AIDA12345")
			},
		},
		{
			name:   "ListUsers wrapping",
			action: "ListUsers",
			payload: User{
				UserName: "admin",
				UserId:   "AIDA99999",
			},
			validate: func(t *testing.T, result any) {
				xmlBytes, err := MarshalToXML(result)
				assert.NoError(t, err)
				xmlStr := string(xmlBytes)
				assert.Contains(t, xmlStr, "ListUsersResponse")
				assert.Contains(t, xmlStr, "ListUsersResult")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateIAMXMLPayload(tt.action, tt.payload)
			assert.NotNil(t, result)
			if tt.validate != nil {
				tt.validate(t, result)
			}
		})
	}
}

func TestKillProcess(t *testing.T) {
	// Create a test process
	cmd := exec.Command("sleep", "60")
	err := cmd.Start()
	require.NoError(t, err)

	pid := cmd.Process.Pid

	// Reap in background so KillProcess can detect termination
	var wg sync.WaitGroup
	wg.Go(func() {
		_ = cmd.Wait()
	})

	err = KillProcess(pid)
	assert.NoError(t, err)
	wg.Wait()

	// Test killing non-existent process
	err = KillProcess(999999)
	assert.Error(t, err, "Should error when killing non-existent process")
}

func TestStopProcess(t *testing.T) {
	// Create and start a test process
	cmd := exec.Command("sleep", "60")
	err := cmd.Start()
	require.NoError(t, err)

	// Write PID file
	testName := "stopprocess-test"
	err = WritePidFile(testName, cmd.Process.Pid)
	require.NoError(t, err)

	// Reap in background so StopProcess can detect termination
	var wg sync.WaitGroup
	wg.Go(func() {
		_ = cmd.Wait()
	})

	err = StopProcess(testName)
	assert.NoError(t, err)
	wg.Wait()

	// Verify PID file was removed
	_, err = ReadPidFile(testName)
	assert.Error(t, err, "PID file should be removed")

	// Test stopping non-existent process
	err = StopProcess("nonexistent-process")
	assert.Error(t, err, "Should error when stopping non-existent process")
}

// Test file extraction process

func TestExtractDiskImageFromFile(t *testing.T) {

	tmpDir, err := os.MkdirTemp("", "hive-utils-test-*")

	t.Log("Temp dir:", tmpDir)

	assert.NoError(t, err, "Temp dir should be created")

	// Sample .xz (fail)
	imagePath, err := ExtractDiskImageFromFile("/tmp/file.xz", tmpDir)

	assert.Empty(t, imagePath, "Should be blank")
	assert.Error(t, err, "Should error")

	// Sample incorrect image (fail)
	imagePath, err = ExtractDiskImageFromFile("../../tests/ebs.json", tmpDir)

	assert.Empty(t, imagePath, "Should be blank")
	assert.Error(t, err, "Should error")
	assert.ErrorContains(t, err, "unsupported filetype")

	// Sample incorrect image (fail)
	imagePath, err = ExtractDiskImageFromFile("../../tests/unit-test-disk-image-bad.raw", tmpDir)

	assert.NotEmpty(t, imagePath, "Should be blank")
	assert.Error(t, err, "Should error")
	assert.ErrorContains(t, err, "no valid disk image found")

	// Sample raw
	imagePath, err = ExtractDiskImageFromFile("../../tests/unit-test-disk-image.raw", tmpDir)

	assert.NotEmpty(t, imagePath, "Should not be blank")
	assert.Contains(t, imagePath, ".raw")

	assert.NoError(t, err, "Should not error")

	_, err = exec.LookPath("tar")

	if err == nil {

		// Sample .tgz
		imagePath, err = ExtractDiskImageFromFile("../../tests/unit-test-disk-image2.tgz", tmpDir)

		assert.NotEmpty(t, imagePath, "Should not be blank")
		assert.Contains(t, imagePath, ".img")

		assert.NoError(t, err, "Should not error")

		// Sample .tar
		imagePath, err = ExtractDiskImageFromFile("../../tests/unit-test-disk-image.tar", tmpDir)

		assert.NotEmpty(t, imagePath, "Should not be blank")
		assert.Contains(t, imagePath, ".raw")

		assert.NoError(t, err, "Should not error")

		// Sample .tar.gz
		imagePath, err = ExtractDiskImageFromFile("../../tests/unit-test-disk-image.tar.gz", tmpDir)

		assert.NotEmpty(t, imagePath, "Should not be blank")
		assert.Contains(t, imagePath, ".raw")

		assert.NoError(t, err, "Should not error")

		// Sample xz
		imagePath, err = ExtractDiskImageFromFile("../../tests/unit-test-disk-image.tar.xz", tmpDir)

		assert.NotEmpty(t, imagePath, "Should not be blank")
		assert.Contains(t, imagePath, ".raw")

		assert.NoError(t, err, "Should not error")

		// Sample tgz
		imagePath, err = ExtractDiskImageFromFile("../../tests/unit-test-disk-image.tgz", tmpDir)

		assert.NotEmpty(t, imagePath, "Should not be blank")
		assert.Contains(t, imagePath, ".raw")

		assert.NoError(t, err, "Should not error")

	} else {
		t.Skip("tar command not found, skipping archive extraction tests")
	}

	//err = os.RemoveAll(tmpDir)
	//assert.NoError(t, err, "Could not remove temp dir")

}

func TestIsSocketURI(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want bool
	}{
		{"Socket suffix", "/run/nbd-vol.sock", true},
		{"Unix prefix", "unix:/run/nbd-vol", true},
		{"Both", "unix:/run/nbd-vol.sock", true},
		{"TCP URI", "nbd://127.0.0.1:9000", false},
		{"Empty", "", false},
		{"Random path", "/tmp/somefile.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsSocketURI(tt.uri))
		})
	}
}

func TestFormatNBDSocketURI(t *testing.T) {
	assert.Equal(t, "nbd:unix:/run/nbd-vol.sock", FormatNBDSocketURI("/run/nbd-vol.sock"))
	assert.Equal(t, "nbd:unix:/tmp/test.sock", FormatNBDSocketURI("/tmp/test.sock"))
}

func TestFormatNBDTCPURI(t *testing.T) {
	assert.Equal(t, "nbd://127.0.0.1:9000", FormatNBDTCPURI("127.0.0.1", 9000))
	assert.Equal(t, "nbd://storage.local:34305", FormatNBDTCPURI("storage.local", 34305))
}

func TestGenerateUniqueSocketFile(t *testing.T) {
	path1, err := GenerateUniqueSocketFile("vol-123")
	require.NoError(t, err)
	assert.Contains(t, path1, "nbd-vol-123-")
	assert.True(t, strings.HasSuffix(path1, ".sock"))

	// Two calls should produce different paths (different timestamps)
	time.Sleep(time.Nanosecond)
	path2, err := GenerateUniqueSocketFile("vol-123")
	require.NoError(t, err)
	assert.NotEqual(t, path1, path2)

	// Empty volume name
	_, err = GenerateUniqueSocketFile("")
	assert.Error(t, err)
}

func TestGenerateXMLPayload(t *testing.T) {
	type Inner struct {
		Name string `locationName:"Name" type:"string"`
	}

	result := GenerateXMLPayload("DescribeInstancesResponse", Inner{Name: "test"})
	assert.NotNil(t, result)

	xmlBytes, err := MarshalToXML(result)
	require.NoError(t, err)
	xmlStr := string(xmlBytes)
	assert.Contains(t, xmlStr, "DescribeInstancesResponse")
	assert.Contains(t, xmlStr, "test")
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		name string
		b    uint64
		want string
	}{
		{"Zero", 0, "0 B"},
		{"Bytes", 512, "512 B"},
		{"KiB", 1024, "1.0 KiB"},
		{"KiB fractional", 1536, "1.5 KiB"},
		{"MiB", 1048576, "1.0 MiB"},
		{"GiB", 1073741824, "1.0 GiB"},
		{"Large GiB", 5368709120, "5.0 GiB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, humanBytes(tt.b))
		})
	}
}

func TestDirExists(t *testing.T) {
	// Existing directory
	assert.True(t, dirExists(os.TempDir()))

	// Non-existent path
	assert.False(t, dirExists("/nonexistent/path/should/not/exist"))

	// File (not a directory)
	tmpFile, err := os.CreateTemp("", "direxists-test-*")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()
	assert.False(t, dirExists(tmpFile.Name()))
}

func TestProgressWriter(t *testing.T) {
	var total int
	pw := progressWriter(func(n int) {
		total += n
	})

	n, err := pw.Write([]byte("hello"))
	assert.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, 5, total)

	n, err = pw.Write([]byte("world!"))
	assert.NoError(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, 11, total)
}

func TestGeneratePidFile_EmptyName(t *testing.T) {
	_, err := GeneratePidFile("")
	assert.Error(t, err)
}

func TestWritePidFileTo(t *testing.T) {
	dir := t.TempDir()

	cmd := exec.Command("cat")
	require.NoError(t, cmd.Start())
	defer cmd.Process.Kill()

	// Write PID to custom directory
	err := WritePidFileTo(dir, "testservice", cmd.Process.Pid)
	require.NoError(t, err)

	// Read it back from the same directory
	pid, err := ReadPidFileFrom(dir, "testservice")
	require.NoError(t, err)
	assert.Equal(t, cmd.Process.Pid, pid)

	// Clean up
	err = RemovePidFileAt(dir, "testservice")
	assert.NoError(t, err)

	// Verify it's gone
	_, err = ReadPidFileFrom(dir, "testservice")
	assert.Error(t, err)
}

func TestWritePidFileTo_EmptyDir(t *testing.T) {
	// With empty dir, should fall back to default pidPath()
	cmd := exec.Command("cat")
	require.NoError(t, cmd.Start())
	defer cmd.Process.Kill()

	err := WritePidFileTo("", "pidto-fallback", cmd.Process.Pid)
	require.NoError(t, err)

	// Should be readable via the default ReadPidFile
	pid, err := ReadPidFile("pidto-fallback")
	require.NoError(t, err)
	assert.Equal(t, cmd.Process.Pid, pid)

	// Clean up
	RemovePidFile("pidto-fallback")
}

func TestStopProcessAt(t *testing.T) {
	dir := t.TempDir()

	// Start a process we can kill
	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())

	// Write PID file
	err := WritePidFileTo(dir, "stopat-test", cmd.Process.Pid)
	require.NoError(t, err)

	// Reap in background so StopProcessAt can detect termination
	var wg sync.WaitGroup
	wg.Go(func() {
		_ = cmd.Wait()
	})

	err = StopProcessAt(dir, "stopat-test")
	assert.NoError(t, err)
	wg.Wait()

	// Verify PID file was removed
	_, err = ReadPidFileFrom(dir, "stopat-test")
	assert.Error(t, err, "PID file should be removed")
}

func TestStopProcessAt_StaleProcess(t *testing.T) {
	dir := t.TempDir()

	// Start a process and let it exit, leaving a stale PID file
	cmd := exec.Command("true")
	require.NoError(t, cmd.Start())
	stalePid := cmd.Process.Pid
	require.NoError(t, cmd.Wait())

	// Write stale PID file
	err := WritePidFileTo(dir, "stale-test", stalePid)
	require.NoError(t, err)

	// StopProcessAt should return an error (process is dead) but still
	// clean up the PID file
	err = StopProcessAt(dir, "stale-test")
	assert.Error(t, err, "should error because process is already dead")

	// PID file must be removed despite the kill error
	_, err = ReadPidFileFrom(dir, "stale-test")
	assert.Error(t, err, "PID file should be removed even when process is already dead")
}

func TestStopProcessAt_NoPidFile(t *testing.T) {
	dir := t.TempDir()
	err := StopProcessAt(dir, "nonexistent")
	assert.Error(t, err, "should error when PID file does not exist")
}

func TestGenerateSocketFile_EmptyName(t *testing.T) {
	_, err := GenerateSocketFile("")
	assert.Error(t, err)
}

func TestSetOOMScore(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("OOM score adjustment only supported on Linux")
	}

	// Read current value so we can set something higher (unprivileged processes
	// can only increase their OOM score, not decrease it)
	pid := os.Getpid()
	current, err := os.ReadFile(fmt.Sprintf("/proc/%d/oom_score_adj", pid))
	if err != nil {
		t.Skipf("Cannot read OOM score: %v", err)
	}

	// Set a positive score (always allowed for unprivileged processes)
	err = SetOOMScore(pid, 100)
	if err != nil {
		t.Skipf("Insufficient permissions to set OOM score: %v", err)
	}

	// Read back and verify
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/oom_score_adj", pid))
	assert.NoError(t, err)
	assert.Equal(t, "100", strings.TrimSpace(string(data)))

	// Best-effort restore (may fail without privileges if original was lower)
	_ = os.WriteFile(fmt.Sprintf("/proc/%d/oom_score_adj", pid), current, 0644)
}

func TestSetOOMScore_InvalidPID(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("OOM score adjustment only supported on Linux")
	}

	err := SetOOMScore(999999999, 100)
	assert.Error(t, err)
}

func TestRuntimeDir(t *testing.T) {
	dir := RuntimeDir()
	assert.NotEmpty(t, dir)
}

func TestPidPath_XDG(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/tmp/test-xdg-runtime")
	assert.Equal(t, "/tmp/test-xdg-runtime", pidPath())
}

func TestPidPath_HomeHiveFallback(t *testing.T) {
	tmpHome := t.TempDir()
	hiveDir := fmt.Sprintf("%s/hive", tmpHome)
	require.NoError(t, os.Mkdir(hiveDir, 0755))

	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("HOME", tmpHome)

	assert.Equal(t, hiveDir, pidPath())
}

func TestPidPath_TempDirFallback(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("HOME", "/nonexistent-home-dir-utils-test")

	assert.Equal(t, os.TempDir(), pidPath())
}

func TestExtractDiskImagePath_NoMatch(t *testing.T) {
	output := []byte("somefile.txt\nanotherfile.conf\n")
	diskimage, err := extractDiskImagePath(t.TempDir(), output)
	assert.Empty(t, diskimage)
	assert.NoError(t, err)
}

func TestExtractDiskImagePath_EmptyOutput(t *testing.T) {
	diskimage, err := extractDiskImagePath(t.TempDir(), []byte{})
	assert.Empty(t, diskimage)
	assert.NoError(t, err)
}

func TestWritePidFile_CreateError(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/nonexistent-dir-utils-test")
	err := WritePidFile("testservice", 12345)
	assert.Error(t, err)
}

func TestRemovePidFileAt_EmptyDir(t *testing.T) {
	err := RemovePidFileAt("", fmt.Sprintf("nonexistent-service-%d", time.Now().UnixNano()))
	assert.Error(t, err)
}

func TestGeneratePidFile_InvalidPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/nonexistent-dir-utils-test")
	path, err := GeneratePidFile("test")
	// GeneratePidFile doesn't check if path exists, just builds it
	assert.NoError(t, err)
	assert.Contains(t, path, "test.pid")
}

func TestReadPidFileFrom_EmptyDir(t *testing.T) {
	_, err := ReadPidFileFrom("", fmt.Sprintf("nonexistent-service-%d", time.Now().UnixNano()))
	assert.Error(t, err)
}
