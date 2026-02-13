package vm

import (
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
	cfg.Architecture = "arm64"

	cmd, err = cfg.Execute()

	// Now expect no error
	assert.NoError(t, err)
	assert.NotNil(t, cmd)

	assert.Contains(t, cmd.Path, "qemu-system-aarch64")
	assert.Equal(t, expectedArgs, cmd.Args[1:])

}

func TestExecute_IOThreadAndCache(t *testing.T) {
	cfg := Config{
		CPUCount:     2,
		Memory:       4096,
		Architecture: "x86_64",
		IOThreads: []IOThread{
			{ID: "ioth-os"},
		},
		Drives: []Drive{
			{
				File:   "nbd:unix:/run/test.sock",
				Format: "raw",
				If:     "none",
				Media:  "disk",
				ID:     "os",
				Cache:  "none",
			},
		},
		Devices: []Device{
			{Value: "virtio-blk-pci,drive=os,iothread=ioth-os,num-queues=2,bootindex=1"},
		},
	}

	cmd, err := cfg.Execute()
	assert.NoError(t, err)
	assert.NotNil(t, cmd)

	args := cmd.Args[1:]

	// Verify iothread object is present
	assert.Contains(t, args, "-object")
	objectIdx := -1
	for i, a := range args {
		if a == "-object" {
			objectIdx = i
			break
		}
	}
	assert.Greater(t, objectIdx, -1)
	assert.Equal(t, "iothread,id=ioth-os", args[objectIdx+1])

	// Verify iothread appears before drives
	driveIdx := -1
	for i, a := range args {
		if a == "-drive" {
			driveIdx = i
			break
		}
	}
	assert.Greater(t, driveIdx, objectIdx, "iothread object must appear before drives")

	// Verify drive includes cache=none
	assert.Equal(t, "file=nbd:unix:/run/test.sock,format=raw,if=none,media=disk,id=os,cache=none", args[driveIdx+1])

	// Verify device includes iothread and num-queues
	deviceIdx := -1
	for i, a := range args {
		if a == "-device" {
			deviceIdx = i
			break
		}
	}
	assert.Greater(t, deviceIdx, -1)
	assert.Equal(t, "virtio-blk-pci,drive=os,iothread=ioth-os,num-queues=2,bootindex=1", args[deviceIdx+1])
}

func TestExecute_NoCacheWhenEmpty(t *testing.T) {
	cfg := Config{
		CPUCount:     1,
		Memory:       512,
		Architecture: "x86_64",
		Drives: []Drive{
			{
				File:   "disk.img",
				Format: "raw",
				ID:     "d0",
			},
		},
	}

	cmd, err := cfg.Execute()
	assert.NoError(t, err)

	// Verify cache= is NOT in the drive string when Cache is empty
	for i, a := range cmd.Args[1:] {
		if a == "-drive" {
			driveStr := cmd.Args[i+2]
			assert.NotContains(t, driveStr, "cache=")
			break
		}
	}
}

func TestExecute_MultipleIOThreads(t *testing.T) {
	cfg := Config{
		CPUCount:     4,
		Memory:       8192,
		Architecture: "x86_64",
		IOThreads: []IOThread{
			{ID: "ioth-os"},
			{ID: "ioth-data"},
		},
		Drives: []Drive{
			{
				File:   "nbd:unix:/run/os.sock",
				Format: "raw",
				If:     "none",
				ID:     "os",
				Cache:  "none",
			},
			{
				File:   "nbd:unix:/run/data.sock",
				Format: "raw",
				If:     "none",
				ID:     "data",
				Cache:  "none",
			},
		},
		Devices: []Device{
			{Value: "virtio-blk-pci,drive=os,iothread=ioth-os,num-queues=4,bootindex=1"},
			{Value: "virtio-blk-pci,drive=data,iothread=ioth-data"},
		},
	}

	cmd, err := cfg.Execute()
	assert.NoError(t, err)

	args := cmd.Args[1:]

	// Count iothread objects
	iothreadCount := 0
	for i, a := range args {
		if a == "-object" && i+1 < len(args) {
			if args[i+1] == "iothread,id=ioth-os" || args[i+1] == "iothread,id=ioth-data" {
				iothreadCount++
			}
		}
	}
	assert.Equal(t, 2, iothreadCount)
}
