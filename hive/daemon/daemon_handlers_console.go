package daemon

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

// handleEC2GetConsoleOutput reads the console log file for an instance and returns
// base64-encoded output matching the AWS GetConsoleOutput API response format.
func (d *Daemon) handleEC2GetConsoleOutput(msg *nats.Msg) {
	slog.Debug("Received GetConsoleOutput request", "subject", msg.Subject, "data", string(msg.Data))

	var input ec2.GetConsoleOutputInput
	if errResp := utils.UnmarshalJsonPayload(&input, msg.Data); errResp != nil {
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}

	if input.InstanceId == nil {
		respondWithError(msg, awserrors.ErrorMissingParameter)
		return
	}

	instanceID := *input.InstanceId

	// Find the instance on this node
	d.Instances.Mu.Lock()
	instance, exists := d.Instances.VMS[instanceID]
	d.Instances.Mu.Unlock()

	if !exists {
		respondWithError(msg, awserrors.ErrorInvalidInstanceIDNotFound)
		return
	}

	logPath := instance.Config.ConsoleLogPath
	var outputData []byte
	var modTime time.Time

	if logPath != "" {
		info, err := os.Stat(logPath)
		if err == nil {
			modTime = info.ModTime()

			data, err := os.ReadFile(logPath)
			if err != nil {
				slog.Error("Failed to read console log", "path", logPath, "err", err)
			} else {
				// Return last 64KB (AWS limit)
				const maxConsoleOutput = 64 * 1024
				if len(data) > maxConsoleOutput {
					data = data[len(data)-maxConsoleOutput:]
				}
				outputData = data
			}
		}
	}

	// Base64-encode the output (AWS returns base64)
	var encodedOutput string
	if len(outputData) > 0 {
		encodedOutput = base64.StdEncoding.EncodeToString(outputData)
	}

	now := time.Now()
	if modTime.IsZero() {
		modTime = now
	}

	output := &ec2.GetConsoleOutputOutput{
		InstanceId: &instanceID,
		Output:     &encodedOutput,
		Timestamp:  &modTime,
	}

	jsonResponse, err := json.Marshal(output)
	if err != nil {
		slog.Error("Failed to marshal GetConsoleOutput response", "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}

	slog.Info("handleEC2GetConsoleOutput completed", "instance_id", instanceID, "output_bytes", len(outputData))
}
