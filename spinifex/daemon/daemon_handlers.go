package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/types"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/mulgadc/spinifex/spinifex/vm"
	"github.com/nats-io/nats.go"
)

// respondWithError sends an error payload for the given error code on the NATS message.
func respondWithError(msg *nats.Msg, errCode string) {
	if err := msg.Respond(utils.GenerateErrorPayload(errCode)); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}

// respondWithJSON marshals data to JSON and sends it as a NATS response.
// On marshal failure it responds with an internal server error.
func respondWithJSON(msg *nats.Msg, data any) {
	jsonResponse, err := json.Marshal(data)
	if err != nil {
		slog.Error("Failed to marshal response", "type", fmt.Sprintf("%T", data), "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}
	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}

// handleNATSRequest is a generic helper for the common unmarshal → service → marshal → respond pattern.
// It extracts the account ID from the NATS message header and passes it to the service function.
func handleNATSRequest[I any, O any](msg *nats.Msg, serviceFn func(*I, string) (*O, error)) {
	accountID := utils.AccountIDFromMsg(msg)
	input := new(I)
	if errResp := utils.UnmarshalJsonPayload(input, msg.Data); errResp != nil {
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}
	output, err := serviceFn(input, accountID)
	if err != nil {
		respondWithError(msg, awserrors.ValidErrorCode(err.Error()))
		return
	}
	respondWithJSON(msg, output)
}

// handleEC2Events processes incoming EC2 instance events (start, stop, terminate, attach-volume)
func (d *Daemon) handleEC2Events(msg *nats.Msg) {
	var command types.EC2InstanceCommand

	if err := json.Unmarshal(msg.Data, &command); err != nil {
		slog.Error("Error unmarshaling EC2 instance command", "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	slog.Debug("Received message", "subject", msg.Subject, "data", string(msg.Data))

	d.Instances.Mu.Lock()
	instance, ok := d.Instances.VMS[command.ID]
	d.Instances.Mu.Unlock()

	if !ok {
		slog.Warn("Instance is not running on this node", "id", command.ID)
		respondWithError(msg, awserrors.ErrorInvalidInstanceIDNotFound)
		return
	}

	// Verify the caller owns this instance
	if !checkInstanceOwnership(msg, command.ID, instance.AccountID) {
		return
	}

	switch {
	case command.Attributes.AttachVolume:
		d.handleAttachVolume(msg, command, instance)
	case command.Attributes.DetachVolume:
		d.handleDetachVolume(msg, command, instance)
	case command.Attributes.StartInstance:
		d.handleStartInstance(msg, command, instance)
	case command.Attributes.RebootInstance:
		d.handleRebootInstance(msg, command, instance)
	case command.Attributes.StopInstance, command.Attributes.TerminateInstance:
		d.handleStopOrTerminateInstance(msg, command, instance)
	default:
		slog.Warn("Unhandled EC2 instance command", "id", command.ID, "attributes", command.Attributes)
		respondWithError(msg, awserrors.ErrorServerInternal)
	}
}

// --- Admin / node management handlers ---

// handleHealthCheck processes NATS health check requests
func (d *Daemon) handleHealthCheck(msg *nats.Msg) {
	configHash, err := d.computeConfigHash()
	if err != nil {
		slog.Error("Failed to compute config hash for health check", "error", err)
		configHash = "error"
	}

	status := "running"
	if !d.ready.Load() {
		status = "starting"
	}

	response := types.NodeHealthResponse{
		Node:       d.node,
		Status:     status,
		ConfigHash: configHash,
		Epoch:      d.clusterConfig.Epoch,
		Uptime:     int64(time.Since(d.startTime).Seconds()),
	}

	respondWithJSON(msg, response)
	slog.Debug("Health check responded", "node", d.node, "epoch", d.clusterConfig.Epoch)
}

// handleNodeDiscover responds to node discovery requests with this node's ID
// Used by the gateway to dynamically discover active hive nodes in the cluster
func (d *Daemon) handleNodeDiscover(msg *nats.Msg) {
	response := types.NodeDiscoverResponse{
		Node: d.node,
	}

	respondWithJSON(msg, response)
	slog.Debug("Node discovery responded", "node", d.node)
}

// daemonIP extracts the IP portion from the daemon host (host:port format).
func (d *Daemon) daemonIP() string {
	host := d.config.Daemon.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// handleNodeStatus responds with this node's status and resource stats.
// Used by the CLI: hive get nodes, hive top nodes.
func (d *Daemon) handleNodeStatus(msg *nats.Msg) {
	totalVCPU, totalMemGB, allocVCPU, allocMemGB, caps := d.resourceMgr.GetResourceStats()

	d.Instances.Mu.Lock()
	vmCount := 0
	for _, v := range d.Instances.VMS {
		if v.Status == vm.StateRunning {
			vmCount++
		}
	}
	d.Instances.Mu.Unlock()

	resp := types.NodeStatusResponse{
		Node:          d.node,
		Status:        "Ready",
		Host:          d.daemonIP(),
		Region:        d.config.Region,
		AZ:            d.config.AZ,
		Uptime:        int64(time.Since(d.startTime).Seconds()),
		Services:      d.config.GetServices(),
		TotalVCPU:     totalVCPU,
		TotalMemGB:    totalMemGB,
		AllocVCPU:     allocVCPU,
		AllocMemGB:    allocMemGB,
		VMCount:       vmCount,
		InstanceTypes: caps,
	}

	respondWithJSON(msg, resp)
}

// handleNodeVMs responds with the list of VMs running on this node.
// Used by the CLI: hive get vms.
func (d *Daemon) handleNodeVMs(msg *nats.Msg) {
	d.Instances.Mu.Lock()
	vms := make([]types.VMInfo, 0, len(d.Instances.VMS))
	for _, v := range d.Instances.VMS {
		info := types.VMInfo{
			InstanceID:   v.ID,
			Status:       string(v.Status),
			InstanceType: v.InstanceType,
		}
		// Get vCPU/memory from the resource manager's instance type info
		if it, ok := d.resourceMgr.instanceTypes[v.InstanceType]; ok {
			info.VCPU = int(instanceTypeVCPUs(it))
			info.MemoryGB = float64(instanceTypeMemoryMiB(it)) / 1024.0
		}
		if v.Instance != nil && v.Instance.LaunchTime != nil {
			info.LaunchTime = v.Instance.LaunchTime.Unix()
		}
		vms = append(vms, info)
	}
	d.Instances.Mu.Unlock()

	resp := types.NodeVMsResponse{
		Node: d.node,
		Host: d.daemonIP(),
		VMs:  vms,
	}

	respondWithJSON(msg, resp)
}
