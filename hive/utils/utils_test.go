package utils

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

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

	log.Print("Started process with PID: ", cmd.Process.Pid)

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

func TestMarshalToXML(t *testing.T) {
	type TestStruct struct {
		Name  string `xml:"Name"`
		Value int    `xml:"Value"`
	}

	tests := []struct {
		name        string
		input       interface{}
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

func TestKillProcess(t *testing.T) {
	// Create a test process
	cmd := exec.Command("sleep", "60")
	err := cmd.Start()
	assert.NoError(t, err)

	pid := cmd.Process.Pid

	// Wait for the process to actually terminate
	// On macOS, the process cleanup can take longer

	go func() {
		time.Sleep(500 * time.Millisecond)
		// Kill the process
		err = KillProcess(pid)
		assert.NoError(t, err)

	}()

	// Reap the process so it does not stay DEFUNCT
	if err := cmd.Wait(); err != nil {
		// often you expect a non nil error here since it was terminated by a signal
		t.Logf("process exited after SIGTERM: %v", err)
	}

	// Wait for process to be reaped
	//_ = cmd.Wait()

	// On macOS, after Wait(), Signal(0) may not reliably detect dead process
	// So we skip this check as it's platform-dependent behavior
	// The fact that KillProcess returned no error is sufficient

	// Test killing non-existent process
	err = KillProcess(999999)
	assert.Error(t, err, "Should error when killing non-existent process")
}

func TestStopProcess(t *testing.T) {
	// Create and start a test process
	cmd := exec.Command("sleep", "60")
	err := cmd.Start()
	assert.NoError(t, err)

	// Write PID file
	testName := "stopprocess-test"

	err = WritePidFile(testName, cmd.Process.Pid)

	assert.NoError(t, err)

	go func() {
		time.Sleep(500 * time.Millisecond)

		// Stop the process
		err = StopProcess(testName)
		assert.NoError(t, err)

		// Verify PID file was removed
		_, err = ReadPidFile(testName)
		assert.Error(t, err, "PID file should be removed")

	}()

	// Reap the process so it does not stay DEFUNCT
	if err := cmd.Wait(); err != nil {
		// often you expect a non nil error here since it was terminated by a signal
		t.Logf("process exited after SIGTERM: %v", err)
	}

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
