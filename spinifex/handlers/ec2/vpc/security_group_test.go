package handlers_ec2_vpc

import (
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestSG(t *testing.T, svc *VPCServiceImpl, vpcID, name string) string {
	t.Helper()
	out, err := svc.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(name),
		Description: aws.String("test sg"),
		VpcId:       aws.String(vpcID),
	}, testAccountID)
	require.NoError(t, err)
	return *out.GroupId
}

// --- CreateSecurityGroup ---

func TestCreateSecurityGroup_Success(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")

	out, err := svc.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("web-sg"),
		Description: aws.String("Web security group"),
		VpcId:       aws.String(vpcID),
	}, testAccountID)
	require.NoError(t, err)
	assert.NotEmpty(t, *out.GroupId)
}

func TestCreateSecurityGroup_MissingGroupName(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
		VpcId: aws.String("vpc-test"),
	}, testAccountID)
	assert.Error(t, err)
}

func TestCreateSecurityGroup_MissingVpcId(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
		GroupName: aws.String("web-sg"),
	}, testAccountID)
	assert.Error(t, err)
}

func TestCreateSecurityGroup_InvalidVpc(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
		GroupName: aws.String("web-sg"),
		VpcId:     aws.String("vpc-nonexistent"),
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidVpcID.NotFound")
}

func TestCreateSecurityGroup_DuplicateName(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")

	_, err := svc.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
		GroupName: aws.String("dup-sg"),
		VpcId:     aws.String(vpcID),
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
		GroupName: aws.String("dup-sg"),
		VpcId:     aws.String(vpcID),
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidGroup.Duplicate")
}

// --- DeleteSecurityGroup ---

func TestDeleteSecurityGroup_Success(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "delete-me")

	_, err := svc.DeleteSecurityGroup(&ec2.DeleteSecurityGroupInput{
		GroupId: aws.String(sgID),
	}, testAccountID)
	require.NoError(t, err)

	// Verify it's gone
	desc, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{}, testAccountID)
	require.NoError(t, err)
	for _, sg := range desc.SecurityGroups {
		assert.NotEqual(t, sgID, *sg.GroupId)
	}
}

func TestDeleteSecurityGroup_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.DeleteSecurityGroup(&ec2.DeleteSecurityGroupInput{
		GroupId: aws.String("sg-nonexistent"),
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidGroup.NotFound")
}

func TestDeleteSecurityGroup_MissingGroupId(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.DeleteSecurityGroup(&ec2.DeleteSecurityGroupInput{}, testAccountID)
	assert.Error(t, err)
}

// --- DescribeSecurityGroups ---

func TestDescribeSecurityGroups_All(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	createTestSG(t, svc, vpcID, "sg-a")
	createTestSG(t, svc, vpcID, "sg-b")

	desc, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, desc.SecurityGroups, 2)
}

func TestDescribeSecurityGroups_ByGroupId(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "target-sg")
	createTestSG(t, svc, vpcID, "other-sg")

	desc, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		GroupIds: []*string{aws.String(sgID)},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, desc.SecurityGroups, 1)
	assert.Equal(t, sgID, *desc.SecurityGroups[0].GroupId)
}

func TestDescribeSecurityGroups_ByGroupName(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	createTestSG(t, svc, vpcID, "find-me")
	createTestSG(t, svc, vpcID, "skip-me")

	desc, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("group-name"), Values: []*string{aws.String("find-me")}},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, desc.SecurityGroups, 1)
	assert.Equal(t, "find-me", *desc.SecurityGroups[0].GroupName)
}

func TestDescribeSecurityGroups_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		GroupIds: []*string{aws.String("sg-nonexistent")},
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidGroup.NotFound")
}

// --- AuthorizeSecurityGroupIngress ---

func TestAuthorizeSecurityGroupIngress_Success(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "ingress-sg")

	proto := "tcp"
	_, err := svc.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []*ec2.IpPermission{
			{
				IpProtocol: &proto,
				FromPort:   aws.Int64(80),
				ToPort:     aws.Int64(80),
				IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
			},
		},
	}, testAccountID)
	require.NoError(t, err)

	// Verify rule was added
	desc, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		GroupIds: []*string{aws.String(sgID)},
	}, testAccountID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(desc.SecurityGroups[0].IpPermissions), 1)
}

func TestAuthorizeSecurityGroupIngress_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String("sg-nonexistent"),
	}, testAccountID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidGroup.NotFound")
}

func TestAuthorizeSecurityGroupIngress_MissingGroupId(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{}, testAccountID)
	assert.Error(t, err)
}

// --- AuthorizeSecurityGroupEgress ---

func TestAuthorizeSecurityGroupEgress_Success(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "egress-sg")

	proto := "tcp"
	_, err := svc.AuthorizeSecurityGroupEgress(&ec2.AuthorizeSecurityGroupEgressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []*ec2.IpPermission{
			{
				IpProtocol: &proto,
				FromPort:   aws.Int64(443),
				ToPort:     aws.Int64(443),
				IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("10.0.0.0/8")}},
			},
		},
	}, testAccountID)
	require.NoError(t, err)
}

func TestAuthorizeSecurityGroupEgress_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.AuthorizeSecurityGroupEgress(&ec2.AuthorizeSecurityGroupEgressInput{
		GroupId: aws.String("sg-nonexistent"),
	}, testAccountID)
	assert.Error(t, err)
}

// --- RevokeSecurityGroupIngress ---

func TestRevokeSecurityGroupIngress_Success(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "revoke-ingress-sg")

	proto := "tcp"
	perm := &ec2.IpPermission{
		IpProtocol: &proto,
		FromPort:   aws.Int64(22),
		ToPort:     aws.Int64(22),
		IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("10.0.0.0/8")}},
	}

	// Add rule
	_, err := svc.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       aws.String(sgID),
		IpPermissions: []*ec2.IpPermission{perm},
	}, testAccountID)
	require.NoError(t, err)

	// Revoke rule
	_, err = svc.RevokeSecurityGroupIngress(&ec2.RevokeSecurityGroupIngressInput{
		GroupId:       aws.String(sgID),
		IpPermissions: []*ec2.IpPermission{perm},
	}, testAccountID)
	require.NoError(t, err)
}

func TestRevokeSecurityGroupIngress_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.RevokeSecurityGroupIngress(&ec2.RevokeSecurityGroupIngressInput{
		GroupId: aws.String("sg-nonexistent"),
	}, testAccountID)
	assert.Error(t, err)
}

// --- RevokeSecurityGroupEgress ---

func TestRevokeSecurityGroupEgress_Success(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "revoke-egress-sg")

	// Revoke default egress rule (0.0.0.0/0 all)
	allProto := "-1"
	_, err := svc.RevokeSecurityGroupEgress(&ec2.RevokeSecurityGroupEgressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []*ec2.IpPermission{
			{
				IpProtocol: &allProto,
				IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
			},
		},
	}, testAccountID)
	require.NoError(t, err)
}

func TestRevokeSecurityGroupEgress_NotFound(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.RevokeSecurityGroupEgress(&ec2.RevokeSecurityGroupEgressInput{
		GroupId: aws.String("sg-nonexistent"),
	}, testAccountID)
	assert.Error(t, err)
}

// --- Helper function tests ---

func TestIpPermissionsToSGRules_TCP(t *testing.T) {
	proto := "tcp"
	perms := []*ec2.IpPermission{
		{
			IpProtocol: &proto,
			FromPort:   aws.Int64(80),
			ToPort:     aws.Int64(80),
			IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("10.0.0.0/8")}},
		},
	}

	rules := ipPermissionsToSGRules(perms)
	require.Len(t, rules, 1)
	assert.Equal(t, "tcp", rules[0].IpProtocol)
	assert.Equal(t, int64(80), rules[0].FromPort)
	assert.Equal(t, int64(80), rules[0].ToPort)
	assert.Equal(t, "10.0.0.0/8", rules[0].CidrIp)
}

func TestIpPermissionsToSGRules_AllTraffic(t *testing.T) {
	allProto := "-1"
	perms := []*ec2.IpPermission{
		{
			IpProtocol: &allProto,
			IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
		},
	}

	rules := ipPermissionsToSGRules(perms)
	require.Len(t, rules, 1)
	assert.Equal(t, "-1", rules[0].IpProtocol)
}

func TestIpPermissionsToSGRules_SourceSG(t *testing.T) {
	proto := "-1"
	perms := []*ec2.IpPermission{
		{
			IpProtocol:       &proto,
			UserIdGroupPairs: []*ec2.UserIdGroupPair{{GroupId: aws.String("sg-source")}},
		},
	}

	rules := ipPermissionsToSGRules(perms)
	require.Len(t, rules, 1)
	assert.Equal(t, "sg-source", rules[0].SourceSG)
}

func TestSGRulesToIpPermissions_Conversion(t *testing.T) {
	rules := []SGRule{
		{IpProtocol: "tcp", FromPort: 443, ToPort: 443, CidrIp: "0.0.0.0/0"},
		{IpProtocol: "-1", SourceSG: "sg-other"},
	}

	perms := sgRulesToIpPermissions(rules)
	require.Len(t, perms, 2)

	// Order is non-deterministic (map-based), find each by protocol
	var tcpPerm, allPerm *ec2.IpPermission
	for _, p := range perms {
		switch *p.IpProtocol {
		case "tcp":
			tcpPerm = p
		case "-1":
			allPerm = p
		}
	}

	require.NotNil(t, tcpPerm, "should have TCP permission")
	assert.Equal(t, int64(443), *tcpPerm.FromPort)
	assert.Len(t, tcpPerm.IpRanges, 1)

	require.NotNil(t, allPerm, "should have all-traffic permission")
	assert.Len(t, allPerm.UserIdGroupPairs, 1)
	assert.Equal(t, "sg-other", *allPerm.UserIdGroupPairs[0].GroupId)
}

func TestSGRuleKey(t *testing.T) {
	rule := SGRule{IpProtocol: "tcp", FromPort: 80, ToPort: 80, CidrIp: "10.0.0.0/8"}
	key := sgRuleKey(rule)
	assert.NotEmpty(t, key)
	assert.Contains(t, key, "tcp")
	assert.Contains(t, key, "80")

	// Same rule should produce same key
	assert.Equal(t, key, sgRuleKey(rule))

	// Different rule should produce different key
	rule2 := SGRule{IpProtocol: "udp", FromPort: 53, ToPort: 53, CidrIp: "10.0.0.0/8"}
	assert.NotEqual(t, key, sgRuleKey(rule2))
}

func TestSGRecordToEC2_Basic(t *testing.T) {
	svc := setupTestVPCService(t)

	record := &SecurityGroupRecord{
		GroupId:     "sg-test",
		GroupName:   "test-group",
		Description: "A test group",
		VpcId:       "vpc-test",
		IngressRules: []SGRule{
			{IpProtocol: "tcp", FromPort: 80, ToPort: 80, CidrIp: "0.0.0.0/0"},
		},
		EgressRules: []SGRule{
			{IpProtocol: "-1", CidrIp: "0.0.0.0/0"},
		},
		Tags: map[string]string{"Name": "test"},
	}

	sg := svc.sgRecordToEC2(record, testAccountID)
	assert.Equal(t, "sg-test", *sg.GroupId)
	assert.Equal(t, "test-group", *sg.GroupName)
	assert.Equal(t, "A test group", *sg.Description)
	assert.Equal(t, "vpc-test", *sg.VpcId)
	assert.Len(t, sg.IpPermissions, 1)
	assert.Len(t, sg.IpPermissionsEgress, 1)
	assert.Len(t, sg.Tags, 1)
}

func TestRemoveSGRules_MatchingRule(t *testing.T) {
	existing := []SGRule{
		{IpProtocol: "tcp", FromPort: 22, ToPort: 22, CidrIp: "10.0.0.0/8"},
		{IpProtocol: "tcp", FromPort: 80, ToPort: 80, CidrIp: "0.0.0.0/0"},
	}
	toRemove := []SGRule{
		{IpProtocol: "tcp", FromPort: 22, ToPort: 22, CidrIp: "10.0.0.0/8"},
	}

	result := removeSGRules(existing, toRemove)
	assert.Len(t, result, 1)
	assert.Equal(t, int64(80), result[0].FromPort)
}

func TestRemoveSGRules_NoMatch(t *testing.T) {
	existing := []SGRule{
		{IpProtocol: "tcp", FromPort: 80, ToPort: 80, CidrIp: "0.0.0.0/0"},
	}
	toRemove := []SGRule{
		{IpProtocol: "udp", FromPort: 53, ToPort: 53, CidrIp: "10.0.0.0/8"},
	}

	result := removeSGRules(existing, toRemove)
	assert.Len(t, result, 1)
}
