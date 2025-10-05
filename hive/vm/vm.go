package vm

import (
	"crypto/rand"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/qmp"
)

/*
qemu-system-x86_64 \
   -enable-kvm \
   -nographic \
   -M ubuntu \
   -cpu host \
   -smp 4 \
   -m 3000 \
   -drive file=/usr/share/ovmf/OVMF.fd,if=pflash,format=raw \
   -drive file=nbd://127.0.0.1:34305/default,format=raw,if=virtio \
   -drive file=nbd://127.0.0.1:39449/default,format=raw,if=none,media=disk,id=debian \
   -device virtio-blk-pci,drive=debian,bootindex=1 \
   -netdev user,id=net0,hostfwd=tcp::2222-:22 \
   -device virtio-rng-pci
*/

type VM struct {
	ID           string `json:"id"`
	PID          int    `json:"pid"`
	PTS          int    `json:"pts"`
	Running      bool   `json:"running"`
	Status       string `json:"status"`
	InstanceType string `json:"instance_type"`
	Config       Config `json:"config"`

	EBSRequests config.EBSRequests `json:"ebs_requests"`

	QMPClient *qmp.QMPClient `json:"-"`

	// User attributes (user initiated stop/delete)
	Attributes qmp.Attributes `json:"attributes"`
}

type Instances struct {
	VMS map[string]*VM `json:"vms"`
	Mu  sync.Mutex     `json:"-"`
}

type NetDev struct {
	Value string `json:"value"`
}

type Device struct {
	Value string `json:"value"`
}

type Drive struct {
	File   string `json:"file"`
	Format string `json:"format"`
	If     string `json:"if"`
	Media  string `json:"media"`
	ID     string `json:"id"`
}

type Config struct {
	Name        string `json:"name"`
	Daemonize   bool   `json:"daemonize"`
	PIDFile     string `json:"pid_file"`
	QMPSocket   string `json:"qmp_socket"`
	EnableKVM   bool   `json:"enable_kvm"`
	NoGraphic   bool   `json:"no_graphic"`
	MachineType string `json:"machine_type"`
	Serial      string `json:"serial"`
	CPUType     string `json:"cpu_type"`
	CPUCount    int    `json:"cpu_count"`
	Memory      int    `json:"memory"`

	Drives []Drive `json:"drives"`

	Devices []Device `json:"devices"`
	NetDevs []NetDev `json:"net_devs"`

	// InstanceType is a friendly name (e.g., t3.micro, t4g.micro)
	InstanceType string `json:"instance_type"`
	Architecture string `json:"architecture"`
}

func (cfg *Config) Execute() (*exec.Cmd, error) {

	args := []string{}

	if cfg.Daemonize {
		args = append(args, "-daemonize")
	}

	if cfg.PIDFile != "" {
		args = append(args, "-pidfile", cfg.PIDFile)
	}

	if cfg.QMPSocket != "" {
		args = append(args, "-qmp", fmt.Sprintf("unix:%s,server,nowait", cfg.QMPSocket))
	}

	if cfg.EnableKVM {
		args = append(args, "-enable-kvm")
	}

	if cfg.NoGraphic {
		args = append(args, "-nographic")
	}

	if cfg.MachineType != "" {
		args = append(args, "-M", cfg.MachineType)
	}

	if cfg.Serial != "" {
		args = append(args, "-serial", cfg.Serial)
	}

	if cfg.CPUType != "" {
		args = append(args, "-cpu", cfg.CPUType)
	}

	if cfg.CPUCount > 0 {
		args = append(args, "-smp", strconv.Itoa(cfg.CPUCount))
	} else {
		return nil, fmt.Errorf("cpu count is required")
	}

	if cfg.Memory > 0 {
		args = append(args, "-m", strconv.Itoa(cfg.Memory))
	} else {
		return nil, fmt.Errorf("memory is required")
	}

	if len(cfg.Drives) == 0 {
		return nil, fmt.Errorf("at least one drive is required")
	}

	for _, drive := range cfg.Drives {

		var opts []string

		//args = append(args, "-drive", fmt.Sprintf("file=%s", drive.File)

		if drive.File != "" {
			opts = append(opts, fmt.Sprintf("file=%s", drive.File))
		}

		if drive.Format != "" {
			opts = append(opts, fmt.Sprintf("format=%s", drive.Format))
		}

		if drive.If != "" {
			opts = append(opts, fmt.Sprintf("if=%s", drive.If))
		}

		if drive.Media != "" {
			opts = append(opts, fmt.Sprintf("media=%s", drive.Media))
		}

		if drive.ID != "" {
			opts = append(opts, fmt.Sprintf("id=%s", drive.ID))
		}

		args = append(args, "-drive", strings.Join(opts, ","))

	}

	for _, device := range cfg.Devices {
		args = append(args, "-device", device.Value)
	}

	for _, netdev := range cfg.NetDevs {
		args = append(args, "-netdev", netdev.Value)
	}

	var qemuArchitecture string

	if cfg.Architecture == "arm" {
		qemuArchitecture = "qemu-system-aarch64"
	} else if cfg.Architecture == "x86_64" {
		qemuArchitecture = "qemu-system-x86_64"
	} else {
		return nil, fmt.Errorf("Architecture missing")
	}

	cmd := exec.Command(qemuArchitecture, args...)

	//cmd.Stdout = os.Stdout
	//cmd.Stderr = os.Stderr

	return cmd, nil
}

func GenerateEC2InstanceID() string {
	const idLength = 17
	const hexChars = "0123456789abcdef"

	bytes := make([]byte, idLength)
	_, err := rand.Read(bytes)
	if err != nil {
		panic("failed to generate random bytes: " + err.Error())
	}

	// Map each byte to a hex digit (0-15)
	for i := range bytes {
		bytes[i] = hexChars[bytes[i]&0x0F]
	}

	return "i-" + string(bytes)
}
