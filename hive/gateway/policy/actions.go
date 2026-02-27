package policy

// IAMAction converts a gateway service name and action name into the
// IAM policy action format "service:ActionName".
// For example: ("ec2", "RunInstances") -> "ec2:RunInstances"
func IAMAction(service, action string) string {
	return service + ":" + action
}

// ec2Actions is the set of known EC2 gateway action names.
var ec2Actions = map[string]bool{
	// Instances
	"DescribeInstances":       true,
	"RunInstances":            true,
	"StartInstances":          true,
	"StopInstances":           true,
	"TerminateInstances":      true,
	"DescribeInstanceTypes":   true,
	"GetConsoleOutput":        true,
	"ModifyInstanceAttribute": true,

	// Key pairs
	"CreateKeyPair":    true,
	"DeleteKeyPair":    true,
	"DescribeKeyPairs": true,
	"ImportKeyPair":    true,

	// Images
	"DescribeImages": true,
	"CreateImage":    true,

	// Regions / AZs
	"DescribeRegions":           true,
	"DescribeAvailabilityZones": true,

	// Volumes
	"DescribeVolumes":      true,
	"ModifyVolume":         true,
	"CreateVolume":         true,
	"DeleteVolume":         true,
	"AttachVolume":         true,
	"DetachVolume":         true,
	"DescribeVolumeStatus": true,

	// Account
	"DescribeAccountAttributes":     true,
	"EnableEbsEncryptionByDefault":  true,
	"DisableEbsEncryptionByDefault": true,
	"GetEbsEncryptionByDefault":     true,
	"GetSerialConsoleAccessStatus":  true,
	"EnableSerialConsoleAccess":     true,
	"DisableSerialConsoleAccess":    true,

	// Tags
	"CreateTags":   true,
	"DeleteTags":   true,
	"DescribeTags": true,

	// Snapshots
	"CreateSnapshot":    true,
	"DeleteSnapshot":    true,
	"DescribeSnapshots": true,
	"CopySnapshot":      true,

	// Internet gateways
	"CreateInternetGateway":    true,
	"DeleteInternetGateway":    true,
	"DescribeInternetGateways": true,
	"AttachInternetGateway":    true,
	"DetachInternetGateway":    true,

	// Egress-only internet gateways
	"CreateEgressOnlyInternetGateway":    true,
	"DeleteEgressOnlyInternetGateway":    true,
	"DescribeEgressOnlyInternetGateways": true,

	// VPC
	"CreateVpc":    true,
	"DeleteVpc":    true,
	"DescribeVpcs": true,

	// Subnets
	"CreateSubnet":    true,
	"DeleteSubnet":    true,
	"DescribeSubnets": true,

	// Network interfaces
	"CreateNetworkInterface":    true,
	"DeleteNetworkInterface":    true,
	"DescribeNetworkInterfaces": true,
}

// iamActions is the set of known IAM gateway action names.
var iamActions = map[string]bool{
	// Users
	"CreateUser": true,
	"GetUser":    true,
	"ListUsers":  true,
	"DeleteUser": true,

	// Access keys
	"CreateAccessKey": true,
	"ListAccessKeys":  true,
	"DeleteAccessKey": true,
	"UpdateAccessKey": true,

	// Policies
	"CreatePolicy":     true,
	"GetPolicy":        true,
	"GetPolicyVersion": true,
	"ListPolicies":     true,
	"DeletePolicy":     true,

	// Policy attachment
	"AttachUserPolicy":         true,
	"DetachUserPolicy":         true,
	"ListAttachedUserPolicies": true,
}

// LookupAction resolves a gateway (service, action) pair to the IAM action
// string used in policy documents. Returns the IAM action string and true
// if found, or ("", false) if the action is unknown.
func LookupAction(service, action string) (string, bool) {
	var known map[string]bool
	switch service {
	case "ec2":
		known = ec2Actions
	case "iam":
		known = iamActions
	default:
		return "", false
	}
	if !known[action] {
		return "", false
	}
	return IAMAction(service, action), true
}
