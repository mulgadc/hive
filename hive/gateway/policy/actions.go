package policy

import "fmt"

// IAMAction converts a gateway service name and action name into the
// IAM policy action format "service:ActionName".
// For example: ("ec2", "RunInstances") → "ec2:RunInstances"
func IAMAction(service, action string) string {
	return fmt.Sprintf("%s:%s", service, action)
}

// EC2Actions maps EC2 gateway action names to their IAM action strings.
var EC2Actions = map[string]string{
	// Instances
	"DescribeInstances":       "ec2:DescribeInstances",
	"RunInstances":            "ec2:RunInstances",
	"StartInstances":          "ec2:StartInstances",
	"StopInstances":           "ec2:StopInstances",
	"TerminateInstances":      "ec2:TerminateInstances",
	"DescribeInstanceTypes":   "ec2:DescribeInstanceTypes",
	"GetConsoleOutput":        "ec2:GetConsoleOutput",
	"ModifyInstanceAttribute": "ec2:ModifyInstanceAttribute",

	// Key pairs
	"CreateKeyPair":    "ec2:CreateKeyPair",
	"DeleteKeyPair":    "ec2:DeleteKeyPair",
	"DescribeKeyPairs": "ec2:DescribeKeyPairs",
	"ImportKeyPair":    "ec2:ImportKeyPair",

	// Images
	"DescribeImages": "ec2:DescribeImages",
	"CreateImage":    "ec2:CreateImage",

	// Regions / AZs
	"DescribeRegions":           "ec2:DescribeRegions",
	"DescribeAvailabilityZones": "ec2:DescribeAvailabilityZones",

	// Volumes
	"DescribeVolumes":      "ec2:DescribeVolumes",
	"ModifyVolume":         "ec2:ModifyVolume",
	"CreateVolume":         "ec2:CreateVolume",
	"DeleteVolume":         "ec2:DeleteVolume",
	"AttachVolume":         "ec2:AttachVolume",
	"DetachVolume":         "ec2:DetachVolume",
	"DescribeVolumeStatus": "ec2:DescribeVolumeStatus",

	// Account
	"DescribeAccountAttributes":     "ec2:DescribeAccountAttributes",
	"EnableEbsEncryptionByDefault":  "ec2:EnableEbsEncryptionByDefault",
	"DisableEbsEncryptionByDefault": "ec2:DisableEbsEncryptionByDefault",
	"GetEbsEncryptionByDefault":     "ec2:GetEbsEncryptionByDefault",
	"GetSerialConsoleAccessStatus":  "ec2:GetSerialConsoleAccessStatus",
	"EnableSerialConsoleAccess":     "ec2:EnableSerialConsoleAccess",
	"DisableSerialConsoleAccess":    "ec2:DisableSerialConsoleAccess",

	// Tags
	"CreateTags":   "ec2:CreateTags",
	"DeleteTags":   "ec2:DeleteTags",
	"DescribeTags": "ec2:DescribeTags",

	// Snapshots
	"CreateSnapshot":    "ec2:CreateSnapshot",
	"DeleteSnapshot":    "ec2:DeleteSnapshot",
	"DescribeSnapshots": "ec2:DescribeSnapshots",
	"CopySnapshot":      "ec2:CopySnapshot",

	// Internet gateways
	"CreateInternetGateway":    "ec2:CreateInternetGateway",
	"DeleteInternetGateway":    "ec2:DeleteInternetGateway",
	"DescribeInternetGateways": "ec2:DescribeInternetGateways",
	"AttachInternetGateway":    "ec2:AttachInternetGateway",
	"DetachInternetGateway":    "ec2:DetachInternetGateway",

	// Egress-only internet gateways
	"CreateEgressOnlyInternetGateway":    "ec2:CreateEgressOnlyInternetGateway",
	"DeleteEgressOnlyInternetGateway":    "ec2:DeleteEgressOnlyInternetGateway",
	"DescribeEgressOnlyInternetGateways": "ec2:DescribeEgressOnlyInternetGateways",

	// VPC
	"CreateVpc":    "ec2:CreateVpc",
	"DeleteVpc":    "ec2:DeleteVpc",
	"DescribeVpcs": "ec2:DescribeVpcs",

	// Subnets
	"CreateSubnet":    "ec2:CreateSubnet",
	"DeleteSubnet":    "ec2:DeleteSubnet",
	"DescribeSubnets": "ec2:DescribeSubnets",

	// Network interfaces
	"CreateNetworkInterface":    "ec2:CreateNetworkInterface",
	"DeleteNetworkInterface":    "ec2:DeleteNetworkInterface",
	"DescribeNetworkInterfaces": "ec2:DescribeNetworkInterfaces",
}

// IAMActions maps IAM gateway action names to their IAM action strings.
var IAMActions = map[string]string{
	// Users
	"CreateUser": "iam:CreateUser",
	"GetUser":    "iam:GetUser",
	"ListUsers":  "iam:ListUsers",
	"DeleteUser": "iam:DeleteUser",

	// Access keys
	"CreateAccessKey": "iam:CreateAccessKey",
	"ListAccessKeys":  "iam:ListAccessKeys",
	"DeleteAccessKey": "iam:DeleteAccessKey",
	"UpdateAccessKey": "iam:UpdateAccessKey",

	// Policies
	"CreatePolicy":     "iam:CreatePolicy",
	"GetPolicy":        "iam:GetPolicy",
	"GetPolicyVersion": "iam:GetPolicyVersion",
	"ListPolicies":     "iam:ListPolicies",
	"DeletePolicy":     "iam:DeletePolicy",

	// Policy attachment
	"AttachUserPolicy":         "iam:AttachUserPolicy",
	"DetachUserPolicy":         "iam:DetachUserPolicy",
	"ListAttachedUserPolicies": "iam:ListAttachedUserPolicies",
}

// LookupAction resolves a gateway (service, action) pair to the IAM action
// string used in policy documents. Returns the IAM action string and true
// if found, or ("", false) if the action is unknown.
func LookupAction(service, action string) (string, bool) {
	var m map[string]string
	switch service {
	case "ec2":
		m = EC2Actions
	case "iam":
		m = IAMActions
	default:
		return "", false
	}
	iamAction, ok := m[action]
	return iamAction, ok
}
