package handlers_ec2_vpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

const (
	KVBucketSecurityGroups        = "spinifex-vpc-security-groups"
	KVBucketSecurityGroupsVersion = 1
)

// SGRule represents a single ingress or egress rule in a security group.
type SGRule struct {
	IpProtocol string `json:"ip_protocol"` // "tcp", "udp", "icmp", "-1" (all)
	FromPort   int64  `json:"from_port"`
	ToPort     int64  `json:"to_port"`
	CidrIp     string `json:"cidr_ip,omitempty"`
	SourceSG   string `json:"source_sg,omitempty"` // Another SG ID for intra-SG rules
}

// SecurityGroupRecord represents a stored security group.
type SecurityGroupRecord struct {
	GroupId      string            `json:"group_id"`
	GroupName    string            `json:"group_name"`
	Description  string            `json:"description"`
	VpcId        string            `json:"vpc_id"`
	IngressRules []SGRule          `json:"ingress_rules"`
	EgressRules  []SGRule          `json:"egress_rules"`
	Tags         map[string]string `json:"tags"`
	CreatedAt    time.Time         `json:"created_at"`
}

// SGEvent is published on vpc.create-sg / vpc.delete-sg / vpc.update-sg for vpcd consumption.
type SGEvent struct {
	GroupId      string   `json:"group_id"`
	VpcId        string   `json:"vpc_id"`
	IngressRules []SGRule `json:"ingress_rules,omitempty"`
	EgressRules  []SGRule `json:"egress_rules,omitempty"`
}

// CreateSecurityGroup creates a new security group in a VPC.
func (s *VPCServiceImpl) CreateSecurityGroup(input *ec2.CreateSecurityGroupInput, accountID string) (*ec2.CreateSecurityGroupOutput, error) {
	if input.GroupName == nil || *input.GroupName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.VpcId == nil || *input.VpcId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	vpcId := *input.VpcId
	groupName := *input.GroupName

	// Verify VPC exists
	if _, err := s.vpcKV.Get(utils.AccountKey(accountID, vpcId)); err != nil {
		return nil, errors.New(awserrors.ErrorInvalidVpcIDNotFound)
	}

	// Check for duplicate group name in the same VPC
	prefix := accountID + "."
	sgKeys, err := s.sgKV.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	for _, k := range sgKeys {
		if k == utils.VersionKey {
			continue
		}
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		entry, err := s.sgKV.Get(k)
		if err != nil {
			continue
		}
		var existing SecurityGroupRecord
		if err := json.Unmarshal(entry.Value(), &existing); err != nil {
			continue
		}
		if existing.VpcId == vpcId && existing.GroupName == groupName {
			return nil, errors.New(awserrors.ErrorInvalidGroupDuplicate)
		}
	}

	groupId := utils.GenerateResourceID("sg")

	description := ""
	if input.Description != nil {
		description = *input.Description
	}

	// Default egress rule: allow all outbound traffic
	defaultEgress := []SGRule{
		{IpProtocol: "-1", FromPort: 0, ToPort: 0, CidrIp: "0.0.0.0/0"},
	}

	record := SecurityGroupRecord{
		GroupId:      groupId,
		GroupName:    groupName,
		Description:  description,
		VpcId:        vpcId,
		IngressRules: []SGRule{},
		EgressRules:  defaultEgress,
		Tags:         make(map[string]string),
		CreatedAt:    time.Now(),
	}

	for _, tagSpec := range input.TagSpecifications {
		if tagSpec.ResourceType != nil && *tagSpec.ResourceType == "security-group" {
			for _, tag := range tagSpec.Tags {
				if tag.Key != nil && tag.Value != nil {
					record.Tags[*tag.Key] = *tag.Value
				}
			}
		}
	}

	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal security group record: %w", err)
	}
	if _, err := s.sgKV.Put(utils.AccountKey(accountID, groupId), data); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreateSecurityGroup completed", "groupId", groupId, "groupName", groupName, "vpcId", vpcId, "accountID", accountID)

	// Publish vpc.create-sg event for vpcd
	s.publishSGEvent("vpc.create-sg", SGEvent{
		GroupId:      groupId,
		VpcId:        vpcId,
		IngressRules: record.IngressRules,
		EgressRules:  record.EgressRules,
	})

	return &ec2.CreateSecurityGroupOutput{
		GroupId: aws.String(groupId),
	}, nil
}

// DeleteSecurityGroup deletes a security group.
func (s *VPCServiceImpl) DeleteSecurityGroup(input *ec2.DeleteSecurityGroupInput, accountID string) (*ec2.DeleteSecurityGroupOutput, error) {
	if input.GroupId == nil || *input.GroupId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	groupId := *input.GroupId
	key := utils.AccountKey(accountID, groupId)

	entry, err := s.sgKV.Get(key)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidGroupNotFound)
	}

	var record SecurityGroupRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	if err := s.sgKV.Delete(key); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeleteSecurityGroup completed", "groupId", groupId, "accountID", accountID)

	// Publish vpc.delete-sg event for vpcd
	s.publishSGEvent("vpc.delete-sg", SGEvent{
		GroupId: groupId,
		VpcId:   record.VpcId,
	})

	return &ec2.DeleteSecurityGroupOutput{}, nil
}

// DescribeSecurityGroups lists security groups with optional filters.
func (s *VPCServiceImpl) DescribeSecurityGroups(input *ec2.DescribeSecurityGroupsInput, accountID string) (*ec2.DescribeSecurityGroupsOutput, error) {
	var groups []*ec2.SecurityGroup

	groupIDs := make(map[string]bool)
	for _, id := range input.GroupIds {
		if id != nil {
			groupIDs[*id] = true
		}
	}

	// Extract VPC ID filter
	vpcIDFilter := ""
	groupNameFilter := ""
	for _, f := range input.Filters {
		if f.Name == nil || len(f.Values) == 0 || f.Values[0] == nil {
			continue
		}
		switch *f.Name {
		case "vpc-id":
			vpcIDFilter = *f.Values[0]
		case "group-name":
			groupNameFilter = *f.Values[0]
		}
	}

	prefix := accountID + "."
	keys, err := s.sgKV.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	for _, key := range keys {
		if key == utils.VersionKey {
			continue
		}
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		entry, err := s.sgKV.Get(key)
		if err != nil {
			slog.Warn("Failed to get security group record", "key", key, "error", err)
			continue
		}

		var record SecurityGroupRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			slog.Warn("Failed to unmarshal security group record", "key", key, "error", err)
			continue
		}

		if len(groupIDs) > 0 && !groupIDs[record.GroupId] {
			continue
		}
		if vpcIDFilter != "" && record.VpcId != vpcIDFilter {
			continue
		}
		if groupNameFilter != "" && record.GroupName != groupNameFilter {
			continue
		}

		groups = append(groups, s.sgRecordToEC2(&record, accountID))
	}

	// If specific group IDs were requested but not found, return error
	if len(groupIDs) > 0 {
		found := make(map[string]bool)
		for _, sg := range groups {
			if sg.GroupId != nil {
				found[*sg.GroupId] = true
			}
		}
		for id := range groupIDs {
			if !found[id] {
				return nil, errors.New(awserrors.ErrorInvalidGroupNotFound)
			}
		}
	}

	slog.Info("DescribeSecurityGroups completed", "count", len(groups), "accountID", accountID)

	return &ec2.DescribeSecurityGroupsOutput{
		SecurityGroups: groups,
	}, nil
}

// AuthorizeSecurityGroupIngress adds ingress rules to a security group.
func (s *VPCServiceImpl) AuthorizeSecurityGroupIngress(input *ec2.AuthorizeSecurityGroupIngressInput, accountID string) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	if input.GroupId == nil || *input.GroupId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	groupId := *input.GroupId
	key := utils.AccountKey(accountID, groupId)

	entry, err := s.sgKV.Get(key)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidGroupNotFound)
	}

	var record SecurityGroupRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Convert AWS IpPermissions to SGRules
	newRules := ipPermissionsToSGRules(input.IpPermissions)
	record.IngressRules = append(record.IngressRules, newRules...)

	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal security group record: %w", err)
	}
	if _, err := s.sgKV.Update(key, data, entry.Revision()); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("AuthorizeSecurityGroupIngress completed", "groupId", groupId, "newRules", len(newRules), "accountID", accountID)

	// Publish vpc.update-sg event for vpcd
	s.publishSGEvent("vpc.update-sg", SGEvent{
		GroupId:      groupId,
		VpcId:        record.VpcId,
		IngressRules: record.IngressRules,
		EgressRules:  record.EgressRules,
	})

	return &ec2.AuthorizeSecurityGroupIngressOutput{
		Return: aws.Bool(true),
	}, nil
}

// AuthorizeSecurityGroupEgress adds egress rules to a security group.
func (s *VPCServiceImpl) AuthorizeSecurityGroupEgress(input *ec2.AuthorizeSecurityGroupEgressInput, accountID string) (*ec2.AuthorizeSecurityGroupEgressOutput, error) {
	if input.GroupId == nil || *input.GroupId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	groupId := *input.GroupId
	key := utils.AccountKey(accountID, groupId)

	entry, err := s.sgKV.Get(key)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidGroupNotFound)
	}

	var record SecurityGroupRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	newRules := ipPermissionsToSGRules(input.IpPermissions)
	record.EgressRules = append(record.EgressRules, newRules...)

	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal security group record: %w", err)
	}
	if _, err := s.sgKV.Update(key, data, entry.Revision()); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("AuthorizeSecurityGroupEgress completed", "groupId", groupId, "newRules", len(newRules), "accountID", accountID)

	s.publishSGEvent("vpc.update-sg", SGEvent{
		GroupId:      groupId,
		VpcId:        record.VpcId,
		IngressRules: record.IngressRules,
		EgressRules:  record.EgressRules,
	})

	return &ec2.AuthorizeSecurityGroupEgressOutput{
		Return: aws.Bool(true),
	}, nil
}

// RevokeSecurityGroupIngress removes ingress rules from a security group.
func (s *VPCServiceImpl) RevokeSecurityGroupIngress(input *ec2.RevokeSecurityGroupIngressInput, accountID string) (*ec2.RevokeSecurityGroupIngressOutput, error) {
	if input.GroupId == nil || *input.GroupId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	groupId := *input.GroupId
	key := utils.AccountKey(accountID, groupId)

	entry, err := s.sgKV.Get(key)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidGroupNotFound)
	}

	var record SecurityGroupRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	revokeRules := ipPermissionsToSGRules(input.IpPermissions)
	record.IngressRules = removeSGRules(record.IngressRules, revokeRules)

	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal security group record: %w", err)
	}
	if _, err := s.sgKV.Update(key, data, entry.Revision()); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("RevokeSecurityGroupIngress completed", "groupId", groupId, "revokedRules", len(revokeRules), "accountID", accountID)

	s.publishSGEvent("vpc.update-sg", SGEvent{
		GroupId:      groupId,
		VpcId:        record.VpcId,
		IngressRules: record.IngressRules,
		EgressRules:  record.EgressRules,
	})

	return &ec2.RevokeSecurityGroupIngressOutput{
		Return: aws.Bool(true),
	}, nil
}

// RevokeSecurityGroupEgress removes egress rules from a security group.
func (s *VPCServiceImpl) RevokeSecurityGroupEgress(input *ec2.RevokeSecurityGroupEgressInput, accountID string) (*ec2.RevokeSecurityGroupEgressOutput, error) {
	if input.GroupId == nil || *input.GroupId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	groupId := *input.GroupId
	key := utils.AccountKey(accountID, groupId)

	entry, err := s.sgKV.Get(key)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidGroupNotFound)
	}

	var record SecurityGroupRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	revokeRules := ipPermissionsToSGRules(input.IpPermissions)
	record.EgressRules = removeSGRules(record.EgressRules, revokeRules)

	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal security group record: %w", err)
	}
	if _, err := s.sgKV.Update(key, data, entry.Revision()); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("RevokeSecurityGroupEgress completed", "groupId", groupId, "revokedRules", len(revokeRules), "accountID", accountID)

	s.publishSGEvent("vpc.update-sg", SGEvent{
		GroupId:      groupId,
		VpcId:        record.VpcId,
		IngressRules: record.IngressRules,
		EgressRules:  record.EgressRules,
	})

	return &ec2.RevokeSecurityGroupEgressOutput{
		Return: aws.Bool(true),
	}, nil
}

// sgRecordToEC2 converts a SecurityGroupRecord to an EC2 SecurityGroup.
func (s *VPCServiceImpl) sgRecordToEC2(record *SecurityGroupRecord, accountID string) *ec2.SecurityGroup {
	sg := &ec2.SecurityGroup{
		GroupId:      aws.String(record.GroupId),
		GroupName:    aws.String(record.GroupName),
		Description:  aws.String(record.Description),
		VpcId:        aws.String(record.VpcId),
		OwnerId:      aws.String(accountID),
		IpPermissions: sgRulesToIpPermissions(record.IngressRules),
		IpPermissionsEgress: sgRulesToIpPermissions(record.EgressRules),
	}

	if len(record.Tags) > 0 {
		tags := make([]*ec2.Tag, 0, len(record.Tags))
		for k, v := range record.Tags {
			tags = append(tags, &ec2.Tag{Key: aws.String(k), Value: aws.String(v)})
		}
		sg.Tags = tags
	}

	return sg
}

// ipPermissionsToSGRules converts AWS IpPermission slice to SGRule slice.
func ipPermissionsToSGRules(perms []*ec2.IpPermission) []SGRule {
	var rules []SGRule
	for _, perm := range perms {
		if perm == nil {
			continue
		}

		proto := "-1"
		if perm.IpProtocol != nil {
			proto = *perm.IpProtocol
		}

		var fromPort, toPort int64
		if perm.FromPort != nil {
			fromPort = *perm.FromPort
		}
		if perm.ToPort != nil {
			toPort = *perm.ToPort
		}

		// One rule per CIDR range
		for _, ipRange := range perm.IpRanges {
			if ipRange.CidrIp == nil {
				continue
			}
			rules = append(rules, SGRule{
				IpProtocol: proto,
				FromPort:   fromPort,
				ToPort:     toPort,
				CidrIp:     *ipRange.CidrIp,
			})
		}

		// One rule per source security group
		for _, pair := range perm.UserIdGroupPairs {
			if pair.GroupId == nil {
				continue
			}
			rules = append(rules, SGRule{
				IpProtocol: proto,
				FromPort:   fromPort,
				ToPort:     toPort,
				SourceSG:   *pair.GroupId,
			})
		}

		// If no ranges and no groups specified, add a rule with no source filter
		if len(perm.IpRanges) == 0 && len(perm.UserIdGroupPairs) == 0 {
			rules = append(rules, SGRule{
				IpProtocol: proto,
				FromPort:   fromPort,
				ToPort:     toPort,
			})
		}
	}
	return rules
}

// sgRulesToIpPermissions converts SGRule slice to AWS IpPermission slice.
func sgRulesToIpPermissions(rules []SGRule) []*ec2.IpPermission {
	// Group rules by protocol+port range
	type permKey struct {
		IpProtocol string
		FromPort   int64
		ToPort     int64
	}

	grouped := make(map[permKey]*ec2.IpPermission)
	for _, rule := range rules {
		key := permKey{IpProtocol: rule.IpProtocol, FromPort: rule.FromPort, ToPort: rule.ToPort}
		perm, exists := grouped[key]
		if !exists {
			perm = &ec2.IpPermission{
				IpProtocol: aws.String(rule.IpProtocol),
				FromPort:   aws.Int64(rule.FromPort),
				ToPort:     aws.Int64(rule.ToPort),
			}
			grouped[key] = perm
		}

		if rule.CidrIp != "" {
			perm.IpRanges = append(perm.IpRanges, &ec2.IpRange{
				CidrIp: aws.String(rule.CidrIp),
			})
		}
		if rule.SourceSG != "" {
			perm.UserIdGroupPairs = append(perm.UserIdGroupPairs, &ec2.UserIdGroupPair{
				GroupId: aws.String(rule.SourceSG),
			})
		}
	}

	result := make([]*ec2.IpPermission, 0, len(grouped))
	for _, perm := range grouped {
		result = append(result, perm)
	}
	return result
}

// removeSGRules removes matching rules from the existing set.
func removeSGRules(existing, toRemove []SGRule) []SGRule {
	removeSet := make(map[string]bool)
	for _, r := range toRemove {
		removeSet[sgRuleKey(r)] = true
	}

	var result []SGRule
	for _, r := range existing {
		if !removeSet[sgRuleKey(r)] {
			result = append(result, r)
		}
	}
	return result
}

// sgRuleKey returns a string key for deduplication/matching of SG rules.
func sgRuleKey(r SGRule) string {
	return fmt.Sprintf("%s:%d:%d:%s:%s", r.IpProtocol, r.FromPort, r.ToPort, r.CidrIp, r.SourceSG)
}

// publishSGEvent publishes a security group lifecycle event to NATS for vpcd consumption.
func (s *VPCServiceImpl) publishSGEvent(topic string, evt SGEvent) {
	if s.natsConn == nil {
		return
	}
	data, err := json.Marshal(evt)
	if err != nil {
		slog.Error("Failed to marshal SG event", "topic", topic, "err", err)
		return
	}
	if err := s.natsConn.Publish(topic, data); err != nil {
		slog.Error("Failed to publish SG event", "topic", topic, "err", err)
	}
}
