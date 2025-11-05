package utils

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/private/protocol/xml/xmlutil"
	"github.com/aws/aws-sdk-go/service/ec2"
)

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
	pidFile.WriteString(fmt.Sprintf("%d", pid))

	return nil
}

func RemovePidFile(serviceName string) error {

	pidPath := pidPath()

	os.Remove(filepath.Join(pidPath, fmt.Sprintf("%s.pid", serviceName)))

	return nil
}

func pidPath() string {
	var pidPath string

	// CHeck if a directory exists

	if os.Getenv("XDG_RUNTIME_DIR") != "" {
		pidPath = os.Getenv("XDG_RUNTIME_DIR")
	} else if dirExists(fmt.Sprintf("%s/%s", os.Getenv("HOME"), "hive")) {
		pidPath = filepath.Join(os.Getenv("HOME"), "hive")
	} else {
		pidPath = os.TempDir()
	}

	return pidPath
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
	RemovePidFile(serviceName)

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

		// If process is still running after 3 checks, force kill
		if checks > 3 {
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
func MarshalToXML(payload interface{}) ([]byte, error) {

	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")

	if err := xmlutil.BuildXML(payload, enc); err != nil {
		slog.Error("BuildXML failed", "err", err)
		return nil, err
	}
	enc.Flush()

	return buf.Bytes(), nil

}

// wrapWithLocation decorates payload with the requested locationName tag.
func GenerateXMLPayload(locationName string, payload interface{}) interface{} {
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

func UnmarshalJsonPayload(input interface{}, jsonData []byte) []byte {

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

func MarshalJsonPayload(input interface{}, jsonData []byte) []byte {

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
			// Import needed: github.com/mulgadc/hive/hive/awserrors
			return fmt.Errorf("InvalidKeyPair.Format")
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
	if strings.HasSuffix(imagefile, ".raw") || strings.HasSuffix(imagefile, ".img") {

		path, err := filepath.Abs(imagepath)

		if err != nil {
			return path, err
		}

		// Validate the specified filename is indeed a disk image / MBR
		err = validateDiskImagePath(path)

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
			"-d",
			imagepath,
		}

		execCmd = "xz"

	} else {
		err = errors.New("unsupported filetype")
		return
	}

	cmd := exec.Command(execCmd, args...)
	output, _ := cmd.Output()

	diskimage, err = extractDiskImagePath(tmpdir, output)

	return

}

func extractDiskImagePath(imagedir string, output []byte) (diskimage string, err error) {

	reader := bytes.NewReader(output)

	r := bufio.NewReader(reader)

	for {
		line, err := r.ReadString('\n')
		line = strings.Replace(line, "\n", "", 1)

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
	output, _ := cmd.Output()

	filetype := strings.Split(string(output), ":")

	if len(filetype) > 1 {

		if strings.Contains(filetype[1], "DOS/MBR boot sector") || strings.Contains(filetype[1], "Linux ") {
			return nil
		}

	}

	return errors.New("no valid disk image found")

}
