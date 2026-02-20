package vm

import (
	"os"
	"slices"
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

// argValue returns the value following flag in args, or "" if not found.
func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func argExists(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

func TestResetNodeLocalState(t *testing.T) {
	v := &VM{
		ID:                    "i-abc123",
		PID:                   12345,
		Running:               true,
		MetadataServerAddress: "127.0.0.1:9999",
		Status:                StateRunning,
	}

	v.ResetNodeLocalState()

	assert.Equal(t, 0, v.PID)
	assert.False(t, v.Running)
	assert.Empty(t, v.MetadataServerAddress)
	assert.NotNil(t, v.QMPClient)
	// ID and Status should be unchanged
	assert.Equal(t, "i-abc123", v.ID)
	assert.Equal(t, StateRunning, v.Status)
}

func TestExecute_PIDFileAndQMPSocket(t *testing.T) {
	cfg := Config{
		CPUCount:     1,
		Memory:       512,
		Architecture: "x86_64",
		PIDFile:      "/run/test.pid",
		QMPSocket:    "/run/test.sock",
		Drives:       []Drive{{File: "disk.img", Format: "raw"}},
	}

	cmd, err := cfg.Execute()
	assert.NoError(t, err)

	args := cmd.Args[1:]
	assert.Equal(t, "/run/test.pid", argValue(args, "-pidfile"))
	assert.Equal(t, "unix:/run/test.sock,server,nowait", argValue(args, "-qmp"))
}

func TestExecute_NoGraphic(t *testing.T) {
	cfg := Config{
		CPUCount:     1,
		Memory:       512,
		Architecture: "x86_64",
		NoGraphic:    true,
		Drives:       []Drive{{File: "disk.img", Format: "raw"}},
	}

	cmd, err := cfg.Execute()
	assert.NoError(t, err)

	args := cmd.Args[1:]
	assert.Equal(t, "none", argValue(args, "-display"))
}

func TestExecute_SerialSocketAndConsoleLog(t *testing.T) {
	cfg := Config{
		CPUCount:       1,
		Memory:         512,
		Architecture:   "x86_64",
		SerialSocket:   "/run/serial.sock",
		ConsoleLogPath: "/var/log/console.log",
		Drives:         []Drive{{File: "disk.img", Format: "raw"}},
	}

	cmd, err := cfg.Execute()
	assert.NoError(t, err)

	args := cmd.Args[1:]
	chardev := argValue(args, "-chardev")
	assert.Contains(t, chardev, "socket,id=console0")
	assert.Contains(t, chardev, "path=/run/serial.sock")
	assert.Contains(t, chardev, "logfile=/var/log/console.log")
	assert.Equal(t, "chardev:console0", argValue(args, "-serial"))
}

func TestExecute_SerialSocketOnly(t *testing.T) {
	cfg := Config{
		CPUCount:     1,
		Memory:       512,
		Architecture: "x86_64",
		SerialSocket: "/run/serial.sock",
		Drives:       []Drive{{File: "disk.img", Format: "raw"}},
	}

	cmd, err := cfg.Execute()
	assert.NoError(t, err)

	args := cmd.Args[1:]
	// When ConsoleLogPath is empty, -chardev and -serial should not appear
	assert.Empty(t, argValue(args, "-chardev"))
	assert.Empty(t, argValue(args, "-serial"))
}

func TestExecute_ConsoleLogPathOnly(t *testing.T) {
	cfg := Config{
		CPUCount:       1,
		Memory:         512,
		Architecture:   "x86_64",
		ConsoleLogPath: "/var/log/console.log",
		Drives:         []Drive{{File: "disk.img", Format: "raw"}},
	}

	cmd, err := cfg.Execute()
	assert.NoError(t, err)

	args := cmd.Args[1:]
	// When SerialSocket is empty, -chardev and -serial should not appear
	assert.Empty(t, argValue(args, "-chardev"))
	assert.Empty(t, argValue(args, "-serial"))
}

func TestExecute_NetDevs(t *testing.T) {
	cfg := Config{
		CPUCount:     1,
		Memory:       512,
		Architecture: "x86_64",
		Drives:       []Drive{{File: "disk.img", Format: "raw"}},
		NetDevs: []NetDev{
			{Value: "tap,id=net0,ifname=tap0,script=no"},
		},
	}

	cmd, err := cfg.Execute()
	assert.NoError(t, err)

	args := cmd.Args[1:]
	assert.Equal(t, "tap,id=net0,ifname=tap0,script=no", argValue(args, "-netdev"))
}

func TestExecute_MachineType_x86(t *testing.T) {
	cfg := Config{
		CPUCount:     1,
		Memory:       512,
		Architecture: "x86_64",
		MachineType:  "q35",
		Drives:       []Drive{{File: "disk.img", Format: "raw"}},
	}

	cmd, err := cfg.Execute()
	assert.NoError(t, err)

	args := cmd.Args[1:]
	assert.Equal(t, "q35", argValue(args, "-M"))
}

func TestExecute_ARM64_Q35(t *testing.T) {
	cfg := Config{
		CPUCount:     1,
		Memory:       512,
		Architecture: "arm64",
		MachineType:  "q35",
		Drives:       []Drive{{File: "disk.img", Format: "raw"}},
	}

	uefiPath := "/usr/share/qemu-efi-aarch64/QEMU_EFI.fd"
	_, uefiErr := os.Stat(uefiPath)
	hasUEFI := uefiErr == nil

	cmd, err := cfg.Execute()

	if hasUEFI {
		// UEFI firmware exists — should succeed with -M virt and -bios
		assert.NoError(t, err)
		assert.NotNil(t, cmd)
		args := cmd.Args[1:]
		assert.Contains(t, cmd.Path, "qemu-system-aarch64")
		assert.Equal(t, "virt", argValue(args, "-M"))
		assert.Equal(t, uefiPath, argValue(args, "-bios"))
	} else {
		// No firmware — error
		assert.Error(t, err)
		assert.Nil(t, cmd)
		assert.Contains(t, err.Error(), "UEFI firmware file not found")
	}
}

func TestExecute_MissingArchitecture(t *testing.T) {
	cfg := Config{
		CPUCount: 1,
		Memory:   512,
		Drives:   []Drive{{File: "disk.img", Format: "raw"}},
	}

	cmd, err := cfg.Execute()
	assert.Error(t, err)
	assert.Nil(t, cmd)
	assert.Contains(t, err.Error(), "architecture missing")
}

func TestExecute_KVMAndCPUType(t *testing.T) {
	cfg := Config{
		CPUCount:     2,
		Memory:       1024,
		Architecture: "x86_64",
		EnableKVM:    true,
		CPUType:      "host",
		Drives:       []Drive{{File: "disk.img", Format: "raw"}},
	}

	cmd, err := cfg.Execute()
	assert.NoError(t, err)

	args := cmd.Args[1:]

	// KVM/CPU flags depend on whether /dev/kvm exists on host
	if _, err := os.Stat("/dev/kvm"); err == nil {
		assert.True(t, argExists(args, "-enable-kvm"))
		assert.Equal(t, "host", argValue(args, "-cpu"))
	} else {
		assert.False(t, argExists(args, "-enable-kvm"))
		assert.Empty(t, argValue(args, "-cpu"))
	}
}

func TestExecute_FullConfig(t *testing.T) {
	cfg := Config{
		Name:           "full-vm",
		PIDFile:        "/run/vm.pid",
		QMPSocket:      "/run/vm.sock",
		NoGraphic:      true,
		MachineType:    "q35",
		ConsoleLogPath: "/var/log/vm.log",
		SerialSocket:   "/run/serial.sock",
		CPUCount:       4,
		Memory:         8192,
		Architecture:   "x86_64",
		IOThreads:      []IOThread{{ID: "io0"}},
		Drives: []Drive{
			{File: "nbd:unix:/run/os.sock", Format: "raw", If: "none", ID: "os", Cache: "none"},
		},
		Devices: []Device{{Value: "virtio-blk-pci,drive=os"}},
		NetDevs: []NetDev{{Value: "user,id=net0"}},
	}

	cmd, err := cfg.Execute()
	assert.NoError(t, err)
	assert.NotNil(t, cmd)
	assert.Contains(t, cmd.Path, "qemu-system-x86_64")

	args := cmd.Args[1:]
	assert.Equal(t, "/run/vm.pid", argValue(args, "-pidfile"))
	assert.Equal(t, "unix:/run/vm.sock,server,nowait", argValue(args, "-qmp"))
	assert.Equal(t, "none", argValue(args, "-display"))
	assert.Equal(t, "q35", argValue(args, "-M"))
	assert.Equal(t, "4", argValue(args, "-smp"))
	assert.Equal(t, "8192", argValue(args, "-m"))
	assert.Contains(t, argValue(args, "-chardev"), "logfile=/var/log/vm.log")
	assert.Equal(t, "chardev:console0", argValue(args, "-serial"))
	assert.Equal(t, "iothread,id=io0", argValue(args, "-object"))
	assert.Equal(t, "user,id=net0", argValue(args, "-netdev"))
}
