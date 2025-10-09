package utils

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

	if err == nil {
		// TODO: Move error codes with vars to errors.go
		return responseError, errors.New("ResponseError detected")
	}

	return responseError, nil

}
