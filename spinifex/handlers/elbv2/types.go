package handlers_elbv2

import "time"

const (
	// LoadBalancer types
	LoadBalancerTypeApplication = "application"
	LoadBalancerTypeNetwork     = "network"

	// LoadBalancer schemes
	SchemeInternetFacing = "internet-facing"
	SchemeInternal       = "internal"

	// LoadBalancer states
	StateProvisioning = "provisioning"
	StateActive       = "active"
	StateFailed       = "failed"

	// Target health states
	TargetHealthInitial   = "initial"
	TargetHealthHealthy   = "healthy"
	TargetHealthUnhealthy = "unhealthy"
	TargetHealthDraining  = "draining"
	TargetHealthUnused    = "unused"

	// Listener protocols (ALB)
	ProtocolHTTP  = "HTTP"
	ProtocolHTTPS = "HTTPS"

	// Listener protocols (NLB)
	ProtocolTCP    = "TCP"
	ProtocolUDP    = "UDP"
	ProtocolTLS    = "TLS"
	ProtocolTCPUDP = "TCP_UDP"

	// Listener action types
	ActionTypeForward = "forward"

	// Default health check values (ALB)
	DefaultHealthCheckInterval           = 30
	DefaultHealthCheckTimeout            = 5
	DefaultHealthyThreshold              = 5
	DefaultUnhealthyThreshold            = 2
	DefaultHealthCheckPath               = "/"
	DefaultHealthCheckPort               = "traffic-port"
	DefaultHealthCheckProtocol           = ProtocolHTTP
	DefaultHealthCheckMatcher            = "200"
	DefaultTargetDeregistrationDelaySecs = 300

	// Default health check values (NLB)
	DefaultNLBHealthCheckInterval = 30
	DefaultNLBHealthCheckTimeout  = 10
	DefaultNLBHealthyThreshold    = 3
	DefaultNLBUnhealthyThreshold  = 3
	DefaultNLBHealthCheckProtocol = ProtocolTCP
	DefaultNLBHealthCheckPort     = "traffic-port"

	// IP address type
	IPAddressTypeIPv4 = "ipv4"
)

// LoadBalancerRecord represents a stored load balancer (ALB or NLB).
type LoadBalancerRecord struct {
	LoadBalancerArn  string            `json:"load_balancer_arn"`
	LoadBalancerID   string            `json:"load_balancer_id"` // Short ID (hex suffix)
	DNSName          string            `json:"dns_name"`
	Name             string            `json:"name"`
	Scheme           string            `json:"scheme"` // "internet-facing" or "internal"
	Type             string            `json:"type"`   // Always "application"
	State            string            `json:"state"`  // "provisioning", "active", "failed"
	VpcId            string            `json:"vpc_id"`
	SecurityGroups   []string          `json:"security_groups"`
	Subnets          []string          `json:"subnets"`
	AvailZones       []AvailZoneInfo   `json:"availability_zones"`
	ENIs             []string          `json:"enis,omitempty"`        // ENI IDs created for this ALB (internal)
	InstanceID       string            `json:"instance_id,omitempty"` // ALB VM instance ID (system-managed)
	VPCIP            string            `json:"vpc_ip,omitempty"`      // VPC private IP of the ALB VM
	ConfigText       string            `json:"config_text,omitempty"` // Pre-computed HAProxy config
	ConfigHash       string            `json:"config_hash,omitempty"` // SHA256 of ConfigText
	LastHeartbeat    time.Time         `json:"last_heartbeat"`        // Last agent heartbeat timestamp
	HostPorts        map[int]int       `json:"host_ports,omitempty"`  // Dev mode: guest port → host port forwarding
	NodeID           string            `json:"node_id"`               // Daemon node running this ALB
	IPAddressType    string            `json:"ip_address_type"`       // "ipv4"
	CrossZoneEnabled bool              `json:"cross_zone_enabled"`    // Default false for NLB, true for ALB
	Tags             map[string]string `json:"tags,omitempty"`
	AccountID        string            `json:"account_id"`
	CreatedAt        time.Time         `json:"created_at"`
}

// AvailZoneInfo tracks subnet-to-AZ mapping for a load balancer.
type AvailZoneInfo struct {
	ZoneName string `json:"zone_name"`
	SubnetId string `json:"subnet_id"`
	PublicIP string `json:"public_ip,omitempty"` // Set for internet-facing ALBs
}

// TargetGroupRecord represents a stored Target Group.
type TargetGroupRecord struct {
	TargetGroupArn string            `json:"target_group_arn"`
	TargetGroupID  string            `json:"target_group_id"` // Short ID (hex suffix)
	Name           string            `json:"name"`
	Protocol       string            `json:"protocol"` // "HTTP" or "HTTPS"
	Port           int64             `json:"port"`     // Default target port
	VpcId          string            `json:"vpc_id"`
	TargetType     string            `json:"target_type"` // "instance" for v1
	HealthCheck    HealthCheckConfig `json:"health_check"`
	Targets        []Target          `json:"targets"`
	Tags           map[string]string `json:"tags,omitempty"`
	AccountID      string            `json:"account_id"`
	CreatedAt      time.Time         `json:"created_at"`
}

// HealthCheckConfig defines health check parameters for a target group.
type HealthCheckConfig struct {
	Protocol           string `json:"protocol"`
	Port               string `json:"port"` // Port number or "traffic-port"
	Path               string `json:"path"`
	IntervalSeconds    int64  `json:"interval_seconds"`
	TimeoutSeconds     int64  `json:"timeout_seconds"`
	HealthyThreshold   int64  `json:"healthy_threshold"`
	UnhealthyThreshold int64  `json:"unhealthy_threshold"`
	Matcher            string `json:"matcher"` // HTTP codes e.g. "200" or "200-299"
}

// DefaultHealthCheck returns a HealthCheckConfig with ALB default values.
func DefaultHealthCheck() HealthCheckConfig {
	return HealthCheckConfig{
		Protocol:           DefaultHealthCheckProtocol,
		Port:               DefaultHealthCheckPort,
		Path:               DefaultHealthCheckPath,
		IntervalSeconds:    DefaultHealthCheckInterval,
		TimeoutSeconds:     DefaultHealthCheckTimeout,
		HealthyThreshold:   DefaultHealthyThreshold,
		UnhealthyThreshold: DefaultUnhealthyThreshold,
		Matcher:            DefaultHealthCheckMatcher,
	}
}

// DefaultNLBHealthCheck returns a HealthCheckConfig with NLB default values.
// NLB uses TCP health checks by default (no path or matcher).
func DefaultNLBHealthCheck() HealthCheckConfig {
	return HealthCheckConfig{
		Protocol:           DefaultNLBHealthCheckProtocol,
		Port:               DefaultNLBHealthCheckPort,
		IntervalSeconds:    DefaultNLBHealthCheckInterval,
		TimeoutSeconds:     DefaultNLBHealthCheckTimeout,
		HealthyThreshold:   DefaultNLBHealthyThreshold,
		UnhealthyThreshold: DefaultNLBUnhealthyThreshold,
	}
}

// Target represents a registered target in a target group.
type Target struct {
	Id          string `json:"id"`           // Instance ID (e.g. i-xxxxx)
	Port        int64  `json:"port"`         // Override port (0 = use TG default)
	HealthState string `json:"health_state"` // "initial", "healthy", "unhealthy", "draining"
	HealthDesc  string `json:"health_desc"`  // Reason for current state
	PrivateIP   string `json:"private_ip"`   // Resolved from instance ENI
}

// ListenerRecord represents a stored Listener.
type ListenerRecord struct {
	ListenerArn     string           `json:"listener_arn"`
	ListenerID      string           `json:"listener_id"` // Short ID (hex suffix)
	LoadBalancerArn string           `json:"load_balancer_arn"`
	Protocol        string           `json:"protocol"` // "HTTP" or "HTTPS"
	Port            int64            `json:"port"`
	DefaultActions  []ListenerAction `json:"default_actions"`
	AccountID       string           `json:"account_id"`
	CreatedAt       time.Time        `json:"created_at"`
}

// ListenerAction defines a listener's default action.
type ListenerAction struct {
	Type           string `json:"type"` // "forward"
	TargetGroupArn string `json:"target_group_arn"`
}
