package vm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExecute(t *testing.T) {

	cfg := Config{
		Name: "test-vm",
	}

	cmd, err := cfg.Execute()

	// Expect error, CPU count required
	assert.Error(t, err)
	assert.ErrorContains(t, err, "cpu count is required")
	assert.Nil(t, cmd)

	cfg.CPUCount = 2

	cmd, err = cfg.Execute()

	// Expect error, Memory required
	assert.Error(t, err)
	assert.ErrorContains(t, err, "memory is required")
	assert.Nil(t, cmd)

	cfg.Memory = 1024

	cmd, err = cfg.Execute()

	// Expect error, at least one drive required
	assert.Error(t, err)
	assert.ErrorContains(t, err, "at least one drive is required")
	assert.Nil(t, cmd)

	cfg.Drives = []Drive{
		{
			File:   "disk.img",
			Format: "qcow2",
		},
	}

	cfg.Architecture = "x86_64"
	cmd, err = cfg.Execute()

	// Now expect no error
	assert.NoError(t, err)
	assert.NotNil(t, cmd)

	expectedArgs := []string{
		"-smp", "2",
		"-m", "1024",
		"-drive", "file=disk.img,format=qcow2",
	}

	assert.Contains(t, cmd.Path, "qemu-system-x86_64")
	assert.Equal(t, expectedArgs, cmd.Args[1:])

	// Toggle Instance type to ARM
	cfg.InstanceType = "t4g.micro"
	cfg.Architecture = "arm"

	cmd, err = cfg.Execute()

	// Now expect no error
	assert.NoError(t, err)
	assert.NotNil(t, cmd)

	assert.Contains(t, cmd.Path, "qemu-system-aarch64")
	assert.Equal(t, expectedArgs, cmd.Args[1:])

}

func TestGenerateEC2InstanceID(t *testing.T) {
	id := GenerateEC2InstanceID()

	assert.Len(t, id, 19)
	assert.True(t, strings.HasPrefix(id, "i-"))

}
