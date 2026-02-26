package daemon

import (
	"encoding/json"
	"log/slog"
	"net"
	"time"

	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/qmp"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/nats-io/nats.go"
)

// respondWithError sends an error payload for the given error code on the NATS message.
func respondWithError(msg *nats.Msg, errCode string) {
	if err := msg.Respond(utils.GenerateErrorPayload(errCode)); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}

// handleNATSRequest is a generic helper for the common unmarshal → service → marshal → respond pattern.
// used for basic requests that don't modify any daemon state, just return the result
func handleNATSRequest[I any, O any](msg *nats.Msg, serviceFn func(*I) (*O, error)) {
	input := new(I)
	if errResp := utils.UnmarshalJsonPayload(input, msg.Data); errResp != nil {
		if err := msg.Respond(errResp); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}
	output, err := serviceFn(input)
	if err != nil {
		if err := msg.Respond(utils.GenerateErrorPayload(awserrors.ValidErrorCode(err.Error()))); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}
	jsonResponse, err := json.Marshal(output)
	if err != nil {
		if err := msg.Respond(utils.GenerateErrorPayload(awserrors.ErrorServerInternal)); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}
	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
}

// handleEC2Events processes incoming EC2 instance events (start, stop, terminate, attach-volume)
func (d *Daemon) handleEC2Events(msg *nats.Msg) {
	var command qmp.Command

	if err := json.Unmarshal(msg.Data, &command); err != nil {
		slog.Error("Error unmarshaling QMP command", "err", err)
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

	switch {
	case command.Attributes.AttachVolume:
		d.handleAttachVolume(msg, command, instance)
	case command.Attributes.DetachVolume:
		d.handleDetachVolume(msg, command, instance)
	case command.Attributes.StartInstance:
		d.handleStartInstance(msg, command, instance)
	case command.Attributes.StopInstance, command.Attributes.TerminateInstance:
		d.handleStopOrTerminateInstance(msg, command, instance)
	default:
		d.handleQMPCommand(msg, command, instance)
	}
}

func (d *Daemon) handleQMPCommand(msg *nats.Msg, command qmp.Command, instance *vm.VM) {
	resp, err := d.SendQMPCommand(instance.QMPClient, command.QMPCommand, instance.ID)
	if err != nil {
		slog.Error("Failed to send QMP command", "err", err)
		respondWithError(msg, awserrors.ErrorServerInternal)
		return
	}

	slog.Debug("RAW QMP Response", "resp", string(resp.Return))

	// Unmarshal the response
	target, ok := qmp.CommandResponseTypes[command.QMPCommand.Execute]
	if !ok {
		slog.Warn("Unhandled QMP command", "cmd", command.QMPCommand.Execute)
		if err := msg.Respond(resp.Return); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}

	if err := json.Unmarshal(resp.Return, target); err != nil {
		slog.Error("Failed to unmarshal QMP response", "cmd", command.QMPCommand.Execute, "err", err)
		if err := msg.Respond(resp.Return); err != nil {
			slog.Error("Failed to respond to NATS request", "err", err)
		}
		return
	}

	// Update attributes and respond
	d.Instances.Mu.Lock()
	instance.Attributes = command.Attributes
	d.Instances.Mu.Unlock()

	if err := d.WriteState(); err != nil {
		slog.Error("Failed to write state to disk", "err", err)
	}

	if err := msg.Respond(resp.Return); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
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

	response := config.NodeHealthResponse{
		Node:       d.node,
		Status:     status,
		ConfigHash: configHash,
		Epoch:      d.clusterConfig.Epoch,
		Uptime:     int64(time.Since(d.startTime).Seconds()),
	}

	jsonResponse, err := json.Marshal(response)
	if err != nil {
		slog.Error("handleHealthCheck failed to marshal response", "err", err)
		return
	}

	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
	slog.Debug("Health check responded", "node", d.node, "epoch", d.clusterConfig.Epoch)
}

// NodeDiscoverResponse is the response for node discovery requests
type NodeDiscoverResponse struct {
	Node string `json:"node"`
}

// handleNodeDiscover responds to node discovery requests with this node's ID
// Used by the gateway to dynamically discover active hive nodes in the cluster
func (d *Daemon) handleNodeDiscover(msg *nats.Msg) {
	response := NodeDiscoverResponse{
		Node: d.node,
	}

	jsonResponse, err := json.Marshal(response)
	if err != nil {
		slog.Error("handleNodeDiscover failed to marshal response", "err", err)
		return
	}

	if err := msg.Respond(jsonResponse); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}
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

	resp := config.NodeStatusResponse{
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

	data, err := json.Marshal(resp)
	if err != nil {
		slog.Error("handleNodeStatus failed to marshal response", "err", err)
		return
	}
	if err := msg.Respond(data); err != nil {
		slog.Error("Failed to respond to hive.node.status", "err", err)
	}
}

// handleNodeVMs responds with the list of VMs running on this node.
// Used by the CLI: hive get vms.
func (d *Daemon) handleNodeVMs(msg *nats.Msg) {
	d.Instances.Mu.Lock()
	vms := make([]config.VMInfo, 0, len(d.Instances.VMS))
	for _, v := range d.Instances.VMS {
		info := config.VMInfo{
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

	resp := config.NodeVMsResponse{
		Node: d.node,
		Host: d.daemonIP(),
		VMs:  vms,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		slog.Error("handleNodeVMs failed to marshal response", "err", err)
		return
	}
	if err := msg.Respond(data); err != nil {
		slog.Error("Failed to respond to hive.node.vms", "err", err)
	}
}
