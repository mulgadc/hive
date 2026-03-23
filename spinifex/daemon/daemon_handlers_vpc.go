package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go/service/ec2"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	"github.com/mulgadc/spinifex/spinifex/admin"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

func (d *Daemon) handleEC2CreateVpc(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.CreateVpc)
}

func (d *Daemon) handleEC2DeleteVpc(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.DeleteVpc)
}

func (d *Daemon) handleEC2DescribeVpcs(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.DescribeVpcs)
}

func (d *Daemon) handleEC2CreateSubnet(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.CreateSubnet)
}

func (d *Daemon) handleEC2DeleteSubnet(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.DeleteSubnet)
}

func (d *Daemon) handleEC2DescribeSubnets(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.DescribeSubnets)
}

func (d *Daemon) handleEC2ModifySubnetAttribute(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.ModifySubnetAttribute)
}

func (d *Daemon) handleEC2CreateNetworkInterface(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.CreateNetworkInterface)
}

func (d *Daemon) handleEC2DeleteNetworkInterface(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.DeleteNetworkInterface)
}

func (d *Daemon) handleEC2DescribeNetworkInterfaces(msg *nats.Msg) {
	handleNATSRequest(msg, d.vpcService.DescribeNetworkInterfaces)
}

// handleAccountCreated creates a default VPC for a newly created account.
func (d *Daemon) handleAccountCreated(msg *nats.Msg) {
	var evt struct {
		AccountID string `json:"account_id"`
	}
	if err := json.Unmarshal(msg.Data, &evt); err != nil {
		slog.Error("Failed to unmarshal account creation event", "error", err)
		return
	}
	if evt.AccountID == "" {
		slog.Error("Account creation event has empty account ID")
		return
	}
	if _, err := d.vpcService.EnsureDefaultVPC(evt.AccountID); err != nil {
		slog.Error("Failed to create default VPC for new account",
			"accountID", evt.AccountID, "error", err)
	}
	d.ensureDefaultVPCInfrastructure()
}

// ensureDefaultVPCInfrastructure creates an IGW and default security group for
// each default VPC that doesn't already have them. Matches AWS behavior where
// the default VPC comes with an attached IGW and a default security group.
func (d *Daemon) ensureDefaultVPCInfrastructure() {
	if d.igwService == nil || d.vpcService == nil {
		return
	}

	for _, accountID := range []string{utils.GlobalAccountID, admin.DefaultAccountID()} {
		// Find the default VPC for this account
		descOut, err := d.vpcService.DescribeVpcs(&ec2.DescribeVpcsInput{}, accountID)
		if err != nil {
			continue
		}
		var defaultVpcId string
		for _, vpc := range descOut.Vpcs {
			if vpc.IsDefault != nil && *vpc.IsDefault {
				defaultVpcId = *vpc.VpcId
				break
			}
		}
		if defaultVpcId == "" {
			continue
		}

		// Check if IGW already attached
		igwOut, err := d.igwService.DescribeInternetGateways(&ec2.DescribeInternetGatewaysInput{}, accountID)
		if err != nil {
			continue
		}
		hasIGW := false
		for _, igw := range igwOut.InternetGateways {
			for _, att := range igw.Attachments {
				if att.VpcId != nil && *att.VpcId == defaultVpcId {
					hasIGW = true
					break
				}
			}
		}
		if !hasIGW {
			// Create and attach an IGW
			createOut, err := d.igwService.CreateInternetGateway(&ec2.CreateInternetGatewayInput{}, accountID)
			if err != nil {
				slog.Error("Failed to create default IGW", "accountID", accountID, "err", err)
				continue
			}
			igwId := *createOut.InternetGateway.InternetGatewayId
			_, err = d.igwService.AttachInternetGateway(&ec2.AttachInternetGatewayInput{
				InternetGatewayId: &igwId,
				VpcId:             &defaultVpcId,
			}, accountID)
			if err != nil {
				slog.Error("Failed to attach default IGW", "igwId", igwId, "vpcId", defaultVpcId, "err", err)
			} else {
				slog.Info("Attached default IGW to default VPC", "igwId", igwId, "vpcId", defaultVpcId, "accountID", accountID)
			}
		}

		// Check if default security group exists for this VPC
		sgOut, err := d.vpcService.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{}, accountID)
		if err != nil {
			continue
		}
		hasDefaultSG := false
		for _, sg := range sgOut.SecurityGroups {
			if sg.VpcId != nil && *sg.VpcId == defaultVpcId && sg.GroupName != nil && *sg.GroupName == "default" {
				hasDefaultSG = true
				break
			}
		}
		if !hasDefaultSG {
			desc := "default VPC security group"
			groupName := "default"
			createSGOut, err := d.vpcService.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
				GroupName:   &groupName,
				Description: &desc,
				VpcId:       &defaultVpcId,
			}, accountID)
			if err != nil {
				slog.Error("Failed to create default security group", "vpcId", defaultVpcId, "err", err)
				continue
			}
			sgId := *createSGOut.GroupId

			// Default SG rules (AWS behavior):
			// - Allow all inbound from same SG (self-referencing)
			// - Allow all outbound to 0.0.0.0/0
			allProto := "-1"
			allCidr := "0.0.0.0/0"
			_, _ = d.vpcService.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
				GroupId: &sgId,
				IpPermissions: []*ec2.IpPermission{
					{
						IpProtocol:       &allProto,
						UserIdGroupPairs: []*ec2.UserIdGroupPair{{GroupId: &sgId}},
					},
				},
			}, accountID)
			_, _ = d.vpcService.AuthorizeSecurityGroupEgress(&ec2.AuthorizeSecurityGroupEgressInput{
				GroupId: &sgId,
				IpPermissions: []*ec2.IpPermission{
					{
						IpProtocol: &allProto,
						IpRanges:   []*ec2.IpRange{{CidrIp: &allCidr}},
					},
				},
			}, accountID)
			slog.Info("Created default security group for default VPC",
				"groupId", sgId, "vpcId", defaultVpcId, "accountID", accountID)
		}
	}
}

// writeBootstrapConfig appends the [bootstrap] section to spinifex.toml if not already present.
// This enables vpcd to reconcile OVN topology on startup for the default VPC.
// configPath is the path to spinifex.toml (e.g., /home/user/spinifex/config/spinifex.toml).
func writeBootstrapConfig(configPath, accountID string, info *handlers_ec2_vpc.DefaultVPCInfo) {
	tomlPath := configPath

	// Check if [bootstrap] already exists
	data, err := os.ReadFile(tomlPath)
	if err != nil {
		slog.Debug("writeBootstrapConfig: could not read spinifex.toml", "err", err)
		return
	}
	if strings.Contains(string(data), "[bootstrap]") {
		return // already present
	}

	section := fmt.Sprintf("\n[bootstrap]\naccount_id  = %q\nvpc_id      = %q\nsubnet_id   = %q\ncidr        = %q\nsubnet_cidr = %q\n",
		accountID, info.VpcId, info.SubnetId, info.Cidr, info.SubnetCidr)

	f, err := os.OpenFile(tomlPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		slog.Warn("writeBootstrapConfig: could not open spinifex.toml for append", "err", err)
		return
	}
	defer f.Close()

	if _, err := f.WriteString(section); err != nil {
		slog.Warn("writeBootstrapConfig: failed to write [bootstrap] section", "err", err)
	} else {
		slog.Info("Wrote [bootstrap] section to spinifex.toml",
			"vpcId", info.VpcId, "subnetId", info.SubnetId)
	}
}
