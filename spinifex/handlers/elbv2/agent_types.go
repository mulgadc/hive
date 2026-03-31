package handlers_elbv2

import "github.com/mulgadc/spinifex/spinifex/albagent"

// ALBAgentHeartbeatInput is sent by the ALB agent on each heartbeat tick.
// The agent includes its health report (HAProxy backend server statuses) so
// the daemon can update target health without polling.
type ALBAgentHeartbeatInput struct {
	LBID    *string                 `locationName:"LBID" type:"string"`
	Servers []*ALBAgentServerStatus `locationName:"Servers" type:"list"`
}

// ALBAgentServerStatus represents a single backend server's health status
// as reported by the ALB agent. Maps to albagent.ServerStatus for processing.
type ALBAgentServerStatus struct {
	Backend *string `locationName:"Backend" type:"string"`
	Server  *string `locationName:"Server" type:"string"`
	Status  *string `locationName:"Status" type:"string"`
}

// ALBAgentHeartbeatOutput is returned to the agent after processing a heartbeat.
type ALBAgentHeartbeatOutput struct {
	Status     *string `type:"string"`
	ConfigHash *string `type:"string"`
}

// GetALBConfigInput is sent by the agent when it detects a config hash change.
type GetALBConfigInput struct {
	LBID *string `locationName:"LBID" type:"string"`
}

// GetALBConfigOutput returns the pre-computed HAProxy config and its hash.
type GetALBConfigOutput struct {
	ConfigText *string `type:"string"`
	ConfigHash *string `type:"string"`
}

// toHealthReport converts the heartbeat input's server list to the albagent
// HealthReport format used by handleHealthReport.
func (in *ALBAgentHeartbeatInput) toHealthReport() albagent.HealthReport {
	report := albagent.HealthReport{}
	if in.LBID != nil {
		report.LBID = *in.LBID
	}
	for _, s := range in.Servers {
		srv := albagent.ServerStatus{}
		if s.Backend != nil {
			srv.Backend = *s.Backend
		}
		if s.Server != nil {
			srv.Server = *s.Server
		}
		if s.Status != nil {
			srv.Status = *s.Status
		}
		report.Servers = append(report.Servers, srv)
	}
	return report
}
