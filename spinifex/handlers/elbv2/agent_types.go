package handlers_elbv2

import "github.com/mulgadc/spinifex/spinifex/lbagent"

// LBAgentHeartbeatInput is sent by the LB agent on each heartbeat tick.
// The agent includes its health report (HAProxy backend server statuses) so
// the daemon can update target health without polling.
type LBAgentHeartbeatInput struct {
	LBID    *string                `locationName:"LBID" type:"string"`
	Servers []*LBAgentServerStatus `locationName:"Servers" type:"list"`
}

// LBAgentServerStatus represents a single backend server's health status
// as reported by the LB agent. Maps to lbagent.ServerStatus for processing.
type LBAgentServerStatus struct {
	Backend *string `locationName:"Backend" type:"string"`
	Server  *string `locationName:"Server" type:"string"`
	Status  *string `locationName:"Status" type:"string"`
}

// LBAgentHeartbeatOutput is returned to the agent after processing a heartbeat.
type LBAgentHeartbeatOutput struct {
	Status     *string `type:"string"`
	ConfigHash *string `type:"string"`
}

// GetLBConfigInput is sent by the agent when it detects a config hash change.
type GetLBConfigInput struct {
	LBID *string `locationName:"LBID" type:"string"`
}

// GetLBConfigOutput returns the pre-computed HAProxy config and its hash.
type GetLBConfigOutput struct {
	ConfigText *string `type:"string"`
	ConfigHash *string `type:"string"`
}

// toHealthReport converts the heartbeat input's server list to the lbagent
// HealthReport format used by handleHealthReportDirect.
func (in *LBAgentHeartbeatInput) toHealthReport() lbagent.HealthReport {
	report := lbagent.HealthReport{}
	if in.LBID != nil {
		report.LBID = *in.LBID
	}
	for _, s := range in.Servers {
		srv := lbagent.ServerStatus{}
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
