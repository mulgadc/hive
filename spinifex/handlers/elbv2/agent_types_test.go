package handlers_elbv2

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/mulgadc/spinifex/spinifex/lbagent"
	"github.com/stretchr/testify/assert"
)

func TestToHealthReport_AllFieldsPopulated(t *testing.T) {
	in := &LBAgentHeartbeatInput{
		LBID: aws.String("lb-abc123"),
		Servers: []*LBAgentServerStatus{
			{Backend: aws.String("tg-1"), Server: aws.String("10.0.0.1:80"), Status: aws.String("UP")},
			{Backend: aws.String("tg-2"), Server: aws.String("10.0.0.2:80"), Status: aws.String("DOWN")},
		},
	}

	report := in.toHealthReport()

	assert.Equal(t, "lb-abc123", report.LBID)
	assert.Len(t, report.Servers, 2)
	assert.Equal(t, lbagent.ServerStatus{Backend: "tg-1", Server: "10.0.0.1:80", Status: "UP"}, report.Servers[0])
	assert.Equal(t, lbagent.ServerStatus{Backend: "tg-2", Server: "10.0.0.2:80", Status: "DOWN"}, report.Servers[1])
}

func TestToHealthReport_NilLBID(t *testing.T) {
	in := &LBAgentHeartbeatInput{
		LBID: nil,
		Servers: []*LBAgentServerStatus{
			{Backend: aws.String("tg-1"), Server: aws.String("10.0.0.1:80"), Status: aws.String("UP")},
		},
	}

	report := in.toHealthReport()

	assert.Equal(t, "", report.LBID)
	assert.Len(t, report.Servers, 1)
}

func TestToHealthReport_NilServers(t *testing.T) {
	in := &LBAgentHeartbeatInput{
		LBID:    aws.String("lb-abc123"),
		Servers: nil,
	}

	report := in.toHealthReport()

	assert.Equal(t, "lb-abc123", report.LBID)
	assert.Empty(t, report.Servers)
}

func TestToHealthReport_EmptyInput(t *testing.T) {
	in := &LBAgentHeartbeatInput{}

	report := in.toHealthReport()

	assert.Equal(t, "", report.LBID)
	assert.Empty(t, report.Servers)
}

func TestToHealthReport_NilServerFields(t *testing.T) {
	in := &LBAgentHeartbeatInput{
		LBID: aws.String("lb-abc123"),
		Servers: []*LBAgentServerStatus{
			{Backend: nil, Server: nil, Status: nil},
			{Backend: aws.String("tg-1"), Server: nil, Status: aws.String("MAINT")},
			{Backend: nil, Server: aws.String("10.0.0.3:80"), Status: nil},
		},
	}

	report := in.toHealthReport()

	assert.Len(t, report.Servers, 3)
	assert.Equal(t, lbagent.ServerStatus{Backend: "", Server: "", Status: ""}, report.Servers[0])
	assert.Equal(t, lbagent.ServerStatus{Backend: "tg-1", Server: "", Status: "MAINT"}, report.Servers[1])
	assert.Equal(t, lbagent.ServerStatus{Backend: "", Server: "10.0.0.3:80", Status: ""}, report.Servers[2])
}
