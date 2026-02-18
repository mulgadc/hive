package utils

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/private/protocol/xml/xmlutil"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/pterm/pterm"
	"golang.org/x/net/http2"
)

// GenerateResourceID generates a unique resource ID with the given prefix.
// Format: {prefix}-{17 hex chars} using crypto/rand.
func GenerateResourceID(prefix string) string {
	b := make([]byte, 9)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return prefix + "-" + hex.EncodeToString(b)[:17]
}

// Helper functions for OS images

var ErrQCOWDetected = errors.New("qcow format detected")

type Images struct {
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Distro       string    `json:"distro"`
	Version      string    `json:"version"`
	Arch         string    `json:"arch"`
	Platform     string    `json:"platform"`
	CreatedAt    time.Time `json:"created_at"`
	URL          string    `json:"url"`
	Checksum     string    `json:"checksum"`
	ChecksumType string    `json:"checksum_type"`
	BootMode     string    `json:"boot_mode"`
	Starred      bool      `json:"starred"`
}

var AvailableImages = map[string]Images{

	"debian-12-x86_64": {
		Name:         "debian-12-x86_64",
		Description:  "Debian 12 (Bookworm) x86_64 cloud image (generic cloud)",
		Distro:       "debian",
		Version:      "12",
		Arch:         "x86_64",
		Platform:     "Linux/UNIX",
		CreatedAt:    time.Date(2025, 10, 6, 0, 0, 0, 0, time.UTC),
		URL:          "https://cdimage.debian.org/cdimage/cloud/bookworm/latest/debian-12-generic-amd64.tar.xz",
		Checksum:     "https://cdimage.debian.org/cdimage/cloud/bookworm/latest/SHA512SUMS",
		ChecksumType: "sha512",
		BootMode:     "bios",
		Starred:      true,
	},

	"debian-12-arm64": {
		Name:         "debian-12-arm64",
		Description:  "Debian 12 (Bookworm) arm64 cloud image (generic cloud)",
		Distro:       "debian",
		Version:      "12",
		Arch:         "arm64",
		Platform:     "Linux/UNIX",
		CreatedAt:    time.Date(2025, 10, 6, 0, 0, 0, 0, time.UTC),
		URL:          "https://cdimage.debian.org/cdimage/cloud/bookworm/latest/debian-12-generic-arm64.tar.xz",
		Checksum:     "https://cdimage.debian.org/cdimage/cloud/bookworm/latest/SHA512SUMS",
		ChecksumType: "sha512",
		BootMode:     "bios",
		Starred:      true,
	},

	// Ubuntu
	"ubuntu-24.04-x86_64": {
		//Ubuntu 24.04 LTS (Noble Numbat)
		Name:         "ubuntu-24.04-x86_64",
		Description:  "Ubuntu 24.04 LTS (Noble Numbat) x86_64 cloud image",
		Distro:       "ubuntu",
		Version:      "24.04",
		Arch:         "x86_64",
		Platform:     "Linux/UNIX",
		CreatedAt:    time.Date(2025, 10, 6, 0, 0, 0, 0, time.UTC),
		URL:          "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img",
		Checksum:     "https://cloud-images.ubuntu.com/noble/current/SHA256SUMS",
		ChecksumType: "sha256",
		BootMode:     "bios",
		Starred:      false,
	},

	"ubuntu-24.04-arm64": {
		//Ubuntu 24.04 LTS (Noble Numbat)
		Name:         "ubuntu-24.04-arm64",
		Description:  "Ubuntu 24.04 LTS (Noble Numbat) arm64 cloud image",
		Distro:       "ubuntu",
		Version:      "24.04",
		Arch:         "arm64",
		Platform:     "Linux/UNIX",
		CreatedAt:    time.Date(2025, 10, 6, 0, 0, 0, 0, time.UTC),
		URL:          "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-arm64.img",
		Checksum:     "https://cloud-images.ubuntu.com/noble/current/SHA256SUMS",
		ChecksumType: "sha256",
		BootMode:     "bios",
		Starred:      false,
	},

	"alpine-3.22.2-x86_64":
	// Alpine Linux (cloud init) x86_64
	{
		Name:         "alpine-3.22.2-x86_64",
		Description:  "Alpine Linux 3.22.2 x86_64 cloud image",
		Distro:       "alpine",
		Version:      "3.22.2",
		Arch:         "x86_64",
		Platform:     "Linux/UNIX",
		CreatedAt:    time.Date(2025, 10, 6, 0, 0, 0, 0, time.UTC),
		URL:          "https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/cloud/generic_alpine-3.22.2-x86_64-bios-cloudinit-r0.qcow2",
		Checksum:     "https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/cloud/generic_alpine-3.22.2-x86_64-bios-cloudinit-r0.qcow2.sha512",
		ChecksumType: "sha512",
		BootMode:     "bios",
		Starred:      false,
	},

	/*
		"alpine-3.22.2-arm64":
		// Alpine Linux (cloud init) arm64 (Requires UEFI boot, TODO)
		{
			Name:         "alpine-3.22.2-arm64",
			Description:  "Alpine Linux 3.22.2 arm64 cloud image",
			Distro:       "alpine",
			Version:      "3.22.2",
			Arch:         "arm64",
			Platform:     "Linux/UNIX",
			CreatedAt:    time.Date(2025, 10, 6, 0, 0, 0, 0, time.UTC),
			URL:          "https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/cloud/gcp_alpine-3.22.2-aarch64-uefi-cloudinit-metal-r0.raw.tar.gz",
			Checksum:     "https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/cloud/gcp_alpine-3.22.2-aarch64-uefi-cloudinit-metal-r0.raw.tar.gz.sha512",
			ChecksumType: "sha512",
			BootMode:     "uefi",
			Starred:      false,
		},
	*/

}

// SetOOMScore sets the OOM score adjustment for a process.
// Score range: -1000 (never kill) to 1000 (always kill first).
// Linux-only; returns an error on non-Linux systems.
func SetOOMScore(pid int, score int) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("OOM score adjustment is only supported on Linux")
	}
	path := fmt.Sprintf("/proc/%d/oom_score_adj", pid)
	return os.WriteFile(path, []byte(strconv.Itoa(score)), 0600)
}

func ReadPidFile(name string) (int, error) {

	pidPath := pidPath()

	pidFile, err := os.ReadFile(filepath.Join(pidPath, fmt.Sprintf("%s.pid", name)))

	if err != nil {
		return 0, err
	}

	// Strip whitespace and /r or /n
	pidFile = bytes.TrimSpace(pidFile)

	return strconv.Atoi(string(pidFile))
}

func GeneratePidFile(name string) (string, error) {

	if name == "" {
		return "", errors.New("name is required")
	}

	pidPath := pidPath()

	if pidPath == "" {
		return "", errors.New("pid path is empty")
	}

	return filepath.Join(pidPath, fmt.Sprintf("%s.pid", name)), nil
}

// GenerateSocketFile generates a socket file path for the given name.
// Deprecated: Use GenerateUniqueSocketFile for new code to ensure uniqueness.
func GenerateSocketFile(name string) (string, error) {

	if name == "" {
		return "", errors.New("name is required")
	}

	pidPath := pidPath()

	if pidPath == "" {
		return "", errors.New("pid path is empty")
	}

	return filepath.Join(pidPath, fmt.Sprintf("%s.sock", name)), nil
}

// GenerateUniqueSocketFile generates a unique socket file path with unix nano timestamp.
// Format: nbd-{volname}-{unixnano}.sock
// This ensures each mount operation gets a unique socket path.
func GenerateUniqueSocketFile(volname string) (string, error) {
	if volname == "" {
		return "", errors.New("volume name is required")
	}

	pidPath := pidPath()
	if pidPath == "" {
		return "", errors.New("pid path is empty")
	}

	timestamp := time.Now().UnixNano()
	filename := fmt.Sprintf("nbd-%s-%d.sock", volname, timestamp)
	return filepath.Join(pidPath, filename), nil
}

// IsSocketURI returns true if the NBD URI is a Unix socket path.
// Socket URIs end with ".sock" or contain "unix:".
func IsSocketURI(nbdURI string) bool {
	return strings.HasSuffix(nbdURI, ".sock") || strings.Contains(nbdURI, "unix:")
}

// FormatNBDSocketURI formats a socket path as an NBD URI for QEMU.
// Returns format: nbd:unix:/path/to/socket.sock
func FormatNBDSocketURI(socketPath string) string {
	return fmt.Sprintf("nbd:unix:%s", socketPath)
}

// FormatNBDTCPURI formats a host:port as an NBD URI for QEMU.
// Returns format: nbd://host:port
func FormatNBDTCPURI(host string, port int) string {
	return fmt.Sprintf("nbd://%s:%d", host, port)
}

// ParseNBDURI parses an NBD URI into its components for QMP blockdev-add.
// Supported formats:
//   - "nbd:unix:/path/to/socket.sock" → serverType="unix", path="/path/to/socket.sock"
//   - "nbd://host:port"               → serverType="inet", host="host", port=<port>
func ParseNBDURI(nbdURI string) (serverType, path, host string, port int, err error) {
	if after, ok := strings.CutPrefix(nbdURI, "nbd:unix:"); ok {
		path = after
		if path == "" {
			return "", "", "", 0, fmt.Errorf("empty socket path in NBD URI: %s", nbdURI)
		}
		return "unix", path, "", 0, nil
	}

	if after, ok := strings.CutPrefix(nbdURI, "nbd://"); ok {
		hostPort := after
		colonIdx := strings.LastIndex(hostPort, ":")
		if colonIdx < 0 {
			return "", "", "", 0, fmt.Errorf("missing port in NBD TCP URI: %s", nbdURI)
		}
		host = hostPort[:colonIdx]
		port, err = strconv.Atoi(hostPort[colonIdx+1:])
		if err != nil {
			return "", "", "", 0, fmt.Errorf("invalid port in NBD URI: %s", nbdURI)
		}
		return "inet", "", host, port, nil
	}

	return "", "", "", 0, fmt.Errorf("unsupported NBD URI format: %s", nbdURI)
}

func WritePidFile(name string, pid int) error {

	// Write PID to file, check XDG, otherwise user home directory ~/hive/
	pidFilename, err := GeneratePidFile(name)

	if err != nil {
		return err
	}

	pidFile, err := os.Create(pidFilename)

	if err != nil {
		return err
	}

	defer pidFile.Close()
	_, err = pidFile.WriteString(fmt.Sprintf("%d", pid))
	if err != nil {
		return err
	}

	return nil
}

// WritePidFileTo writes a PID file to a specific directory. If dir is empty,
// falls back to the default pidPath(). Used by services that know their own
// data directory (e.g. predastore's BasePath) to avoid PID file collisions
// when multiple nodes run on the same host.
func WritePidFileTo(dir string, name string, pid int) error {
	if dir == "" {
		return WritePidFile(name, pid)
	}

	pidFilename := filepath.Join(dir, fmt.Sprintf("%s.pid", name))

	pidFile, err := os.Create(pidFilename)
	if err != nil {
		return err
	}

	defer pidFile.Close()
	_, err = pidFile.WriteString(fmt.Sprintf("%d", pid))
	return err
}

// ReadPidFileFrom reads a PID from a file in a specific directory. If dir is
// empty, falls back to the default pidPath().
func ReadPidFileFrom(dir string, name string) (int, error) {
	if dir == "" {
		return ReadPidFile(name)
	}

	data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%s.pid", name)))
	if err != nil {
		return 0, err
	}

	data = bytes.TrimSpace(data)
	return strconv.Atoi(string(data))
}

// RemovePidFileAt removes a PID file from a specific directory. If dir is
// empty, falls back to the default pidPath().
func RemovePidFileAt(dir string, name string) error {
	if dir == "" {
		return RemovePidFile(name)
	}
	return os.Remove(filepath.Join(dir, fmt.Sprintf("%s.pid", name)))
}

// StopProcessAt stops a process using a PID file in a specific directory.
// If dir is empty, falls back to the default pidPath(). The PID file is
// always removed, even if the process is already dead, to prevent stale
// PID files from accumulating across restarts.
func StopProcessAt(dir string, name string) error {
	pid, err := ReadPidFileFrom(dir, name)
	if err != nil {
		return err
	}

	killErr := KillProcess(pid)

	// Always remove the PID file to avoid stale entries. If the process is
	// already dead, the PID file is stale and must be cleaned up.
	if removeErr := RemovePidFileAt(dir, name); removeErr != nil && killErr == nil {
		return removeErr
	}

	return killErr
}

func RemovePidFile(serviceName string) error {

	pidPath := pidPath()

	err := os.Remove(filepath.Join(pidPath, fmt.Sprintf("%s.pid", serviceName)))
	if err != nil {
		return err
	}

	return nil
}

// RuntimeDir returns the runtime directory used for PID files, sockets, and logs.
func RuntimeDir() string {
	return pidPath()
}

func pidPath() string {
	if os.Getenv("XDG_RUNTIME_DIR") != "" {
		return os.Getenv("XDG_RUNTIME_DIR")
	}
	if dirExists(fmt.Sprintf("%s/%s", os.Getenv("HOME"), "hive")) {
		return filepath.Join(os.Getenv("HOME"), "hive")
	}
	return os.TempDir()
}

func StopProcess(serviceName string) error {
	pid, err := ReadPidFile(serviceName)
	if err != nil {
		return err
	}

	err = KillProcess(pid)
	if err != nil {
		return err
	}

	// Remove PID file
	err = RemovePidFile(serviceName)
	if err != nil {
		return err
	}

	return nil
}

func KillProcess(pid int) error {

	process, err := os.FindProcess(pid)

	if err != nil {
		return err
	}

	// Send SIGTERM first (graceful)
	err = process.Signal(syscall.SIGTERM)
	if err != nil {
		return err
	}

	// Check process terminated

	checks := 0
	for {
		time.Sleep(1 * time.Second)
		process, err = os.FindProcess(pid)

		if err != nil {
			return err
		}

		err = process.Signal(syscall.Signal(0))

		if err != nil {
			// Process terminated, break
			break
		}

		checks++

		// If process is still running after 120 seconds, force kill
		if checks > 120 {
			err = process.Kill() // SIGKILL

			if err != nil {
				return err
			}

			break
		}
	}

	return nil

}

func WaitForPidFileRemoval(instanceID string, timeout time.Duration) error {
	timeoutCh := time.After(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCh:
			return fmt.Errorf("timeout waiting for PID file to be removed for instance %s", instanceID)
		case <-ticker.C:
			_, err := ReadPidFile(instanceID)
			if err != nil {
				// PID file no longer exists
				return nil
			}
		}
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		return false
	}
	return info.IsDir()
}

// Convert interface to XML
func MarshalToXML(payload any) ([]byte, error) {

	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	// enc.Indent("", "  ")

	if err := xmlutil.BuildXML(payload, enc); err != nil {
		slog.Error("BuildXML failed", "err", err)
		return nil, err
	}

	if err := enc.Flush(); err != nil {
		slog.Error("Flush failed", "err", err)
		return nil, err
	}

	return buf.Bytes(), nil

}

// wrapWithLocation decorates payload with the requested locationName tag.
func GenerateXMLPayload(locationName string, payload any) any {
	t := reflect.StructOf([]reflect.StructField{
		{
			Name: "Value",
			Type: reflect.TypeOf(payload),
			Tag:  reflect.StructTag(`locationName:"` + locationName + `"`),
		},
	})

	v := reflect.New(t).Elem()
	v.Field(0).Set(reflect.ValueOf(payload))
	return v.Interface()
}

// GenerateIAMXMLPayload wraps IAM output with proper *Result element structure.
// IAM responses have format: <ActionResponse><ActionResult>content</ActionResult><ResponseMetadata>...</ResponseMetadata></ActionResponse>
func GenerateIAMXMLPayload(action string, payload any) any {
	// First wrap with ActionResult (e.g., CreateUserResult)
	resultName := action + "Result"
	resultWrapper := reflect.StructOf([]reflect.StructField{
		{
			Name: "Result",
			Type: reflect.TypeOf(payload),
			Tag:  reflect.StructTag(`locationName:"` + resultName + `"`),
		},
	})
	resultV := reflect.New(resultWrapper).Elem()
	resultV.Field(0).Set(reflect.ValueOf(payload))

	// Then wrap with ActionResponse (e.g., CreateUserResponse)
	responseName := action + "Response"
	responseWrapper := reflect.StructOf([]reflect.StructField{
		{
			Name: "Response",
			Type: resultWrapper,
			Tag:  reflect.StructTag(`locationName:"` + responseName + `"`),
		},
	})
	responseV := reflect.New(responseWrapper).Elem()
	responseV.Field(0).Set(resultV)

	return responseV.Interface()
}

// Generate JSON Error Payload
func GenerateErrorPayload(code string) (jsonResponse []byte) {

	var responseError ec2.ResponseError
	responseError.Code = aws.String(code)

	// Return as JSON, to simulate the NATS response
	jsonResponse, err := json.Marshal(responseError)
	if err != nil {
		slog.Error("GenerateErrorPayload could not marshal JSON payload", "err", err)
		return nil
	}

	return

}

// Validate the payload is an ec2.ResponseError
func ValidateErrorPayload(payload []byte) (responseError ec2.ResponseError, err error) {

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()

	err = decoder.Decode(&responseError)

	if err == nil && responseError.Code != nil {
		// Successfully decoded as ResponseError AND has a non-nil Code field
		// This is a real error response
		return responseError, errors.New("ResponseError detected")
	}

	// Either failed to decode (not an error structure) or Code is nil (empty valid response)
	return responseError, nil

}

// Unmarshal payload

func UnmarshalJsonPayload(input any, jsonData []byte) []byte {

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	// input is already a pointer, don't take address again
	err := decoder.Decode(input)
	if err != nil {
		// TODO: Move error codes with vars to errors.go
		return GenerateErrorPayload("ValidationError")
	}

	return nil

}

func MarshalJsonPayload(input any, jsonData []byte) []byte {

	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()

	// input is already a pointer, don't take address again
	err := decoder.Decode(input)
	if err != nil {
		// TODO: Move error codes with vars to errors.go
		return GenerateErrorPayload("ValidationError")
	}

	return nil

}

// ValidateKeyPairName validates that a key pair name only contains allowed characters:
// - Uppercase A-Z
// - Lowercase a-z
// - Digits 0-9
// - Hyphen (-)
// - Underscore (_)
// - Period (.)
// This prevents path traversal attacks and invalid characters like /etc/passwd, ../../../, etc.
// Returns ErrorInvalidKeyPairFormat if validation fails
func ValidateKeyPairName(name string) error {
	if name == "" {
		return errors.New("key name cannot be empty")
	}

	// Check each character is in the allowed set
	for _, char := range name {
		valid := (char >= 'A' && char <= 'Z') ||
			(char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') ||
			char == '-' ||
			char == '_' ||
			char == '.'

		if !valid {
			return errors.New(awserrors.ErrorInvalidKeyPairFormat)
		}
	}

	return nil
}

// AMI / image extraction utils
func ExtractDiskImageFromFile(imagepath string, tmpdir string) (diskimage string, err error) {

	var args []string
	var execCmd string

	// Confirm file exists
	_, err = os.Stat(imagepath)

	if err != nil {
		return
	}

	// Extract the filepath
	imagefile := filepath.Base(imagepath)

	// Provide the full path to the specified file
	//imagedir, err := filepath.Abs(filepath.Dir(imagepath))

	//if err != nil {
	//	return
	//}

	// Already in raw/image formt, confirm the file contains a valid disk image/MBR
	if strings.HasSuffix(imagefile, ".raw") || strings.HasSuffix(imagefile, ".img") || strings.HasSuffix(imagefile, ".qcow2") || strings.HasSuffix(imagefile, ".qcow") {

		path, err := filepath.Abs(imagepath)

		if err != nil {
			return path, err
		}

		// Validate the specified filename is indeed a disk image / MBR
		err = validateDiskImagePath(path)

		// Check error response

		if errors.Is(err, ErrQCOWDetected) {

			//fmt.Println("Extracting raw disk image from qcow2 file", "file", imagepath)

			extractpath := fmt.Sprintf("%s/%s", tmpdir, imagefile)
			extractpath = strings.TrimSuffix(extractpath, ".qcow2") + ".raw"

			args = []string{
				"convert",
				"-f",
				"qcow2",
				"-O",
				"raw",
				imagepath,
				"-C",
				extractpath,
			}

			execCmd = "qemu-img"

			//fmt.Println("Executing command:", "cmd", execCmd, "args", args)

			cmd := exec.Command(execCmd, args...)
			_, err = cmd.Output()

			if err != nil {
				return path, err
			}

			return extractpath, nil

		}

		return path, err

	} else if strings.HasSuffix(imagefile, ".tar.xz") {

		args = []string{
			"xfvJ",
			imagepath,
			"-C",
			tmpdir,
		}

		execCmd = "tar"

	} else if strings.HasSuffix(imagefile, ".tar.gz") || strings.HasSuffix(imagefile, ".tgz") {

		args = []string{
			"xfvz",
			imagepath,
			"-C",
			tmpdir,
		}

		execCmd = "tar"

	} else if strings.HasSuffix(imagefile, ".tar") {

		args = []string{
			"xfv",
			imagepath,
			"-C",
			tmpdir,
		}

		execCmd = "tar"

	} else if strings.HasSuffix(imagefile, ".xz") {

		args = []string{
			"-dk",
			imagepath,
		}

		execCmd = "xz"

	} else {
		err = errors.New("unsupported filetype")
		return
	}

	cmd := exec.Command(execCmd, args...)
	output, err := cmd.CombinedOutput()

	if err != nil {
		return
	}

	diskimage, err = extractDiskImagePath(tmpdir, output)

	return

}

func extractDiskImagePath(imagedir string, output []byte) (diskimage string, err error) {

	reader := bytes.NewReader(output)

	r := bufio.NewReader(reader)

	for {
		line, err := r.ReadString('\n')
		line = strings.Replace(line, "\n", "", 1)

		// MacOS tar, filenames begin with `x FILE` (to STDERR)
		if runtime.GOOS == "darwin" && strings.HasPrefix(line, "x ") {
			line = strings.Replace(line, "x ", "", 1)

		}

		if strings.HasSuffix(line, ".raw") || strings.HasSuffix(line, ".img") {
			diskimage := fmt.Sprintf("%s/%s", imagedir, line)

			err = validateDiskImagePath(diskimage)

			return diskimage, err
		}

		if err != nil && err.Error() == "EOF" {
			break
		}
	}

	return

}

func validateDiskImagePath(diskimage string) (err error) {

	args := []string{
		diskimage,
	}

	cmd := exec.Command("file", args...)
	output, _ := cmd.CombinedOutput()

	filetype := strings.Split(string(output), ":")

	if len(filetype) > 1 {

		if strings.Contains(filetype[1], "DOS/MBR boot sector") || strings.Contains(filetype[1], "Linux ") {
			return nil
		} else if strings.Contains(filetype[1], "QEMU QCOW") {
			return ErrQCOWDetected
		}

	}

	return errors.New("no valid disk image found")

}

func CreateS3Client(cfg *config.Config) *s3.S3 {

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2", "http/1.1"},
		},
		ForceAttemptHTTP2: true,
	}

	// CRITICAL: Configure HTTP/2 support with custom TLS config
	if err := http2.ConfigureTransport(tr); err != nil {
		slog.Warn("Failed to configure HTTP/2", "error", err)
	}

	httpClient := &http.Client{Transport: tr}

	sess := session.Must(session.NewSession(&aws.Config{
		Region:           aws.String(cfg.Predastore.Region),
		Endpoint:         aws.String(fmt.Sprintf("https://%s", cfg.Predastore.Host)),
		Credentials:      credentials.NewStaticCredentials(cfg.AccessKey, cfg.SecretKey, ""),
		S3ForcePathStyle: aws.Bool(true),
		DisableSSL:       aws.Bool(false),
		HTTPClient:       httpClient,
	}))

	return s3.New(sess)

}

// Download helper

func DownloadFileWithProgress(url string, name string, filename string, timeout time.Duration) (err error) {

	// Context with optional timeout and Ctrl+C cancel
	ctx, cancel := context.WithCancel(context.Background())
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	intCh := make(chan os.Signal, 1)
	signal.Notify(intCh, os.Interrupt)
	go func() {
		<-intCh
		cancel()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("request error: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("http error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}

	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("file create error: %v", err)
	}
	defer f.Close()

	cl := resp.ContentLength

	// Known size: progress bar with total
	if cl > 0 {
		bar, _ := pterm.DefaultProgressbar.
			WithTitle(fmt.Sprintf("Downloading %s", name)).
			WithTotal(int(cl)).
			Start()

		var written int64
		reader := io.TeeReader(resp.Body, progressWriter(func(n int) {
			written += int64(n)
			// Update progress bar with the number of bytes read in this chunk
			bar.Add(n)
		}))

		_, err = io.Copy(f, reader)
		_, _ = bar.Stop()
		if err != nil {
			return fmt.Errorf("copy error: %v", err)
		}

		pterm.Printf("Saved %s (%s)\n", filename, humanBytes(SafeInt64ToUint64(written)))
		return

	} else {

		// Unknown size: spinner that shows bytes downloaded
		spin, _ := pterm.DefaultSpinner.
			WithText("Downloading (size unknown)...").
			Start()
		var written int64
		reader := io.TeeReader(resp.Body, progressWriter(func(n int) {
			written += int64(n)
			spin.UpdateText(fmt.Sprintf("Downloading %s (%s) ...", name, humanBytes(SafeInt64ToUint64(written))))
		}))
		_, err = io.Copy(f, reader)
		_ = spin.Stop()

		if err != nil {
			return fmt.Errorf("copy error: %v", err)
		}

		pterm.Printf("Saved %s (%s)\n", filename, humanBytes(SafeInt64ToUint64(written)))

	}

	return nil
}

// progressWriter turns byte counts into a callback for UI updates.
type progressWriter func(n int)

func (pw progressWriter) Write(p []byte) (int, error) {
	pw(len(p))
	return len(p), nil
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPEZY"[exp])
}
