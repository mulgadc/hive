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
	// - close stdio so parent doesn’t block on pipes
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	// Start (non-blocking). If command is missing, error.
	if err := cmd.Start(); err != nil {
		assert.Fail(t, "Failed to start command", err)
	}

	// IMPORTANT: reap the child to avoid zombies.
	// Since we’re “backgrounding” it, do Wait() in a goroutine.
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
