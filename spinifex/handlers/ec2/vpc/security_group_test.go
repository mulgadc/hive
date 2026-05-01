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

// --- SG-rule input validation (Finding 1: OVN ACL match-expression injection) ---

func TestValidateCidrIp(t *testing.T) {
	cases := []struct {
		name    string
		cidr    string
		wantErr bool
	}{
		{"canonical /32", "1.2.3.4/32", false},
		{"canonical /8", "10.0.0.0/8", false},
		{"canonical /16", "10.0.0.0/16", false},
		{"any", "0.0.0.0/0", false},
		{"host bits set", "10.0.0.5/8", true},
		{"missing prefix", "10.0.0.0", true},
		{"empty", "", true},
		{"injection || or", "1.2.3.4/32 || ip4.src == 0.0.0.0/0", true},
		{"injection &&", "10.0.0.0/8 && ip4.src == 0.0.0.0/0", true},
		{"injection drop", "0.0.0.0/0; drop", true},
		{"injection jndi", "${jndi:ldap://x}", true},
		{"injection newline", "10.0.0.0/8\n outport == @other", true},
		{"non-ascii", "10.0.0.0/8 ", true},
		{"bad mask", "10.0.0.0/33", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateCidrIp(c.cidr)
			if c.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateSourceSG(t *testing.T) {
	cases := []struct {
		name    string
		sg      string
		wantErr bool
	}{
		{"valid 17 hex", "sg-0123456789abcdef0", false},
		{"empty", "", true},
		{"too short", "sg-abc", true},
		{"too long", "sg-0123456789abcdef01", true},
		{"uppercase rejected", "sg-0123456789ABCDEF0", true},
		{"missing prefix", "0123456789abcdef0", true},
		{"injection or", "sg-abc || outport == @other", true},
		{"injection and", "sg-abc && ip4.src == 0.0.0.0/0", true},
		{"injection at", "@other_pg", true},
		{"injection dollar", "$injected", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateSourceSG(c.sg)
			if c.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAuthorizeSecurityGroupIngress_RejectsCidrIpInjection(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "inj-sg")

	payloads := []string{
		"1.2.3.4/32 || ip4.src == 0.0.0.0/0",
		"0.0.0.0/0; drop",
		"${jndi:ldap://x}",
		"10.0.0.0/8\n outport == @other",
		"10.0.0.0/8 && ip4.src == 0.0.0.0/0",
		"10.0.0.0/5", // host bits set (non-canonical)
		"10.0.0.0",   // missing prefix
		"not-a-cidr",
	}

	proto := "tcp"
	for _, p := range payloads {
		t.Run(p, func(t *testing.T) {
			_, err := svc.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
				GroupId: aws.String(sgID),
				IpPermissions: []*ec2.IpPermission{
					{
						IpProtocol: &proto,
						FromPort:   aws.Int64(80),
						ToPort:     aws.Int64(80),
						IpRanges:   []*ec2.IpRange{{CidrIp: aws.String(p)}},
					},
				},
			}, testAccountID)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "InvalidParameterValue")
		})
	}
}

func TestAuthorizeSecurityGroupIngress_RejectsSourceSGInjection(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "inj-sg-src")

	payloads := []string{
		"sg-abc || outport == @other",
		"sg-abc && ip4.src == 0.0.0.0/0",
		"@other_pg",
		"sg-source",           // legacy short form, no longer accepted
		"sg-ABCDEF1234567890", // uppercase
	}

	proto := "-1"
	for _, p := range payloads {
		t.Run(p, func(t *testing.T) {
			_, err := svc.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
				GroupId: aws.String(sgID),
				IpPermissions: []*ec2.IpPermission{
					{
						IpProtocol:       &proto,
						UserIdGroupPairs: []*ec2.UserIdGroupPair{{GroupId: aws.String(p)}},
					},
				},
			}, testAccountID)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "InvalidParameterValue")
		})
	}
}

func TestAuthorizeSecurityGroupEgress_RejectsCidrIpInjection(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "inj-sg-egress")

	proto := "tcp"
	_, err := svc.AuthorizeSecurityGroupEgress(&ec2.AuthorizeSecurityGroupEgressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []*ec2.IpPermission{
			{
				IpProtocol: &proto,
				FromPort:   aws.Int64(443),
				ToPort:     aws.Int64(443),
				IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("10.0.0.0/8 && ip4.dst == 0.0.0.0/0")}},
			},
		},
	}, testAccountID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

func TestRevokeSecurityGroupIngress_RejectsInjection(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "inj-sg-revoke")

	proto := "tcp"
	_, err := svc.RevokeSecurityGroupIngress(&ec2.RevokeSecurityGroupIngressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []*ec2.IpPermission{
			{
				IpProtocol: &proto,
				FromPort:   aws.Int64(80),
				ToPort:     aws.Int64(80),
				IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("0.0.0.0/0; drop")}},
			},
		},
	}, testAccountID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

func TestRevokeSecurityGroupEgress_RejectsInjection(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "inj-sg-revoke-egress")

	proto := "-1"
	_, err := svc.RevokeSecurityGroupEgress(&ec2.RevokeSecurityGroupEgressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []*ec2.IpPermission{
			{
				IpProtocol:       &proto,
				UserIdGroupPairs: []*ec2.UserIdGroupPair{{GroupId: aws.String("sg-abc || outport == @other")}},
			},
		},
	}, testAccountID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidParameterValue")
}

func TestAuthorizeSecurityGroupIngress_AcceptsValidSourceSG(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "valid-src-sg")
	srcSG := createTestSG(t, svc, vpcID, "valid-target-sg")

	proto := "-1"
	_, err := svc.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []*ec2.IpPermission{
			{
				IpProtocol:       &proto,
				UserIdGroupPairs: []*ec2.UserIdGroupPair{{GroupId: aws.String(srcSG)}},
			},
		},
	}, testAccountID)
	require.NoError(t, err)
}

// --- DescribeSecurityGroups filter tests ---

func TestDescribeSecurityGroups_FilterByGroupId(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "target-sg")
	createTestSG(t, svc, vpcID, "other-sg")

	out, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("group-id"), Values: []*string{aws.String(sgID)}},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, out.SecurityGroups, 1)
	assert.Equal(t, sgID, *out.SecurityGroups[0].GroupId)
}

func TestDescribeSecurityGroups_FilterByDescription(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")

	_, err := svc.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("web-sg"),
		Description: aws.String("Web server security group"),
		VpcId:       aws.String(vpcID),
	}, testAccountID)
	require.NoError(t, err)

	_, err = svc.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("db-sg"),
		Description: aws.String("Database security group"),
		VpcId:       aws.String(vpcID),
	}, testAccountID)
	require.NoError(t, err)

	out, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("description"), Values: []*string{aws.String("Web server security group")}},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, out.SecurityGroups, 1)
	assert.Equal(t, "web-sg", *out.SecurityGroups[0].GroupName)
}

func TestDescribeSecurityGroups_FilterByVpcId(t *testing.T) {
	svc := setupTestVPCService(t)
	vpc1 := createTestVPC(t, svc, "10.0.0.0/16")
	vpc2 := createTestVPC(t, svc, "172.16.0.0/16")
	createTestSG(t, svc, vpc1, "sg-in-vpc1")
	createTestSG(t, svc, vpc2, "sg-in-vpc2")

	out, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("vpc-id"), Values: []*string{aws.String(vpc1)}},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, out.SecurityGroups, 1)
	assert.Equal(t, vpc1, *out.SecurityGroups[0].VpcId)
}

func TestDescribeSecurityGroups_FilterByIpPermissionCidr(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	sgID := createTestSG(t, svc, vpcID, "cidr-sg")

	proto := "tcp"
	_, err := svc.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []*ec2.IpPermission{
			{
				IpProtocol: &proto,
				FromPort:   aws.Int64(80),
				ToPort:     aws.Int64(80),
				IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("10.0.0.0/8")}},
			},
		},
	}, testAccountID)
	require.NoError(t, err)

	// Create another SG without the ingress rule
	createTestSG(t, svc, vpcID, "no-cidr-sg")

	out, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("ip-permission.cidr"), Values: []*string{aws.String("10.0.0.0/8")}},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, out.SecurityGroups, 1)
	assert.Equal(t, sgID, *out.SecurityGroups[0].GroupId)
}

func TestDescribeSecurityGroups_FilterMultipleValues_OR(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	createTestSG(t, svc, vpcID, "sg-alpha")
	createTestSG(t, svc, vpcID, "sg-beta")
	createTestSG(t, svc, vpcID, "sg-gamma")

	out, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("group-name"), Values: []*string{aws.String("sg-alpha"), aws.String("sg-gamma")}},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, out.SecurityGroups, 2)
}

func TestDescribeSecurityGroups_FilterMultipleFilters_AND(t *testing.T) {
	svc := setupTestVPCService(t)
	vpc1 := createTestVPC(t, svc, "10.0.0.0/16")
	vpc2 := createTestVPC(t, svc, "172.16.0.0/16")
	createTestSG(t, svc, vpc1, "same-name")
	createTestSG(t, svc, vpc2, "same-name")

	out, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("group-name"), Values: []*string{aws.String("same-name")}},
			{Name: aws.String("vpc-id"), Values: []*string{aws.String(vpc1)}},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, out.SecurityGroups, 1)
	assert.Equal(t, vpc1, *out.SecurityGroups[0].VpcId)
}

func TestDescribeSecurityGroups_FilterUnknownName_Error(t *testing.T) {
	svc := setupTestVPCService(t)

	_, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("bogus-filter"), Values: []*string{aws.String("x")}},
		},
	}, testAccountID)
	assert.Error(t, err)
}

func TestDescribeSecurityGroups_FilterWildcard(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	createTestSG(t, svc, vpcID, "prod-web")
	createTestSG(t, svc, vpcID, "prod-api")
	createTestSG(t, svc, vpcID, "staging-web")

	out, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("group-name"), Values: []*string{aws.String("prod-*")}},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, out.SecurityGroups, 2)
}

func TestDescribeSecurityGroups_FilterByTag(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")

	out, err := svc.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("tagged-sg"),
		Description: aws.String("tagged"),
		VpcId:       aws.String(vpcID),
		TagSpecifications: []*ec2.TagSpecification{
			{
				ResourceType: aws.String("security-group"),
				Tags:         []*ec2.Tag{{Key: aws.String("Env"), Value: aws.String("prod")}},
			},
		},
	}, testAccountID)
	require.NoError(t, err)

	createTestSG(t, svc, vpcID, "untagged-sg")

	desc, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("tag:Env"), Values: []*string{aws.String("prod")}},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Len(t, desc.SecurityGroups, 1)
	assert.Equal(t, *out.GroupId, *desc.SecurityGroups[0].GroupId)
}

func TestDescribeSecurityGroups_FilterNoResults(t *testing.T) {
	svc := setupTestVPCService(t)
	vpcID := createTestVPC(t, svc, "10.0.0.0/16")
	createTestSG(t, svc, vpcID, "my-sg")

	out, err := svc.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{Name: aws.String("group-name"), Values: []*string{aws.String("nonexistent")}},
		},
	}, testAccountID)
	require.NoError(t, err)
	assert.Empty(t, out.SecurityGroups)
}
