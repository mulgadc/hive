package handlers_elbv2

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/config"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

const (
	// elbv2ManagedByTag is the tag key used to mark ENIs as system-managed by ELBv2.
	elbv2ManagedByTag = "spinifex:managed-by"
	// elbv2ManagedByValue is the tag value for ELBv2-managed ENIs.
	elbv2ManagedByValue = "elbv2"
	// elbv2LBTag is the tag key storing the parent LB ARN on managed ENIs.
	elbv2LBTag = "spinifex:lb-arn"
)

// Ensure ELBv2ServiceImpl implements ELBv2Service at compile time.
var _ ELBv2Service = (*ELBv2ServiceImpl)(nil)

// ELBv2ServiceImpl implements ELBv2 operations with NATS JetStream persistence.
type ELBv2ServiceImpl struct {
	config     *config.Config
	store      *Store
	vpcService *handlers_ec2_vpc.VPCServiceImpl // nil-safe: ENI ops skipped when nil (e.g. in tests)
	nodeID     string
	region     string
}

// NewELBv2ServiceImplWithNATS creates an ELBv2 service backed by JetStream KV.
func NewELBv2ServiceImplWithNATS(cfg *config.Config, nc *nats.Conn) (*ELBv2ServiceImpl, error) {
	store, err := NewStore(nc)
	if err != nil {
		return nil, fmt.Errorf("failed to create ELBv2 store: %w", err)
	}

	region := "us-east-1"
	nodeID := ""
	if cfg != nil {
		if cfg.Region != "" {
			region = cfg.Region
		}
		nodeID = cfg.Node
	}

	return &ELBv2ServiceImpl{
		config: cfg,
		store:  store,
		nodeID: nodeID,
		region: region,
	}, nil
}

// SetVPCService sets the VPC service for ENI management. Called by the daemon
// after both services are initialized.
func (s *ELBv2ServiceImpl) SetVPCService(vpcService *handlers_ec2_vpc.VPCServiceImpl) {
	s.vpcService = vpcService
}

// buildLBArn constructs an ALB ARN from components.
func buildLBArn(region, accountID, name, lbID string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/app/%s/%s", region, accountID, name, lbID)
}

// buildTGArn constructs a target group ARN from components.
func buildTGArn(region, accountID, name, tgID string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:targetgroup/%s/%s", region, accountID, name, tgID)
}

// buildListenerArn constructs a listener ARN from components.
func buildListenerArn(region, accountID, lbName, lbID, listenerID string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:listener/app/%s/%s/%s", region, accountID, lbName, lbID, listenerID)
}

// --- Load Balancer operations ---

func (s *ELBv2ServiceImpl) CreateLoadBalancer(input *elbv2.CreateLoadBalancerInput, accountID string) (*elbv2.CreateLoadBalancerOutput, error) {
	if input.Name == nil || *input.Name == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	name := *input.Name

	// Check for duplicate name
	existing, err := s.store.GetLoadBalancerByName(name)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if existing != nil {
		return nil, errors.New(awserrors.ErrorELBv2DuplicateLoadBalancer)
	}

	scheme := SchemeInternetFacing
	if input.Scheme != nil && *input.Scheme != "" {
		scheme = *input.Scheme
		if scheme != SchemeInternetFacing && scheme != SchemeInternal {
			return nil, errors.New(awserrors.ErrorELBv2InvalidScheme)
		}
	}

	lbID := utils.GenerateResourceID("lb")
	lbArn := buildLBArn(s.region, accountID, name, lbID)
	dnsName := fmt.Sprintf("%s-%s.%s.elb.spinifex.local", name, lbID, s.region)

	var subnets []string
	for _, sn := range input.Subnets {
		if sn != nil {
			subnets = append(subnets, *sn)
		}
	}

	var securityGroups []string
	for _, sg := range input.SecurityGroups {
		if sg != nil {
			securityGroups = append(securityGroups, *sg)
		}
	}

	tags := make(map[string]string)
	for _, tag := range input.Tags {
		if tag.Key != nil && tag.Value != nil {
			tags[*tag.Key] = *tag.Value
		}
	}

	// Create ENIs in each subnet (when VPC service is available)
	var eniIDs []string
	var availZones []AvailZoneInfo
	vpcID := ""
	if s.vpcService != nil && len(subnets) > 0 {
		for _, subnetID := range subnets {
			eniOut, eniErr := s.vpcService.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
				SubnetId:    aws.String(subnetID),
				Description: aws.String(fmt.Sprintf("ELB app/%s/%s", name, lbID)),
				TagSpecifications: []*ec2.TagSpecification{
					{
						ResourceType: aws.String("network-interface"),
						Tags: []*ec2.Tag{
							{Key: aws.String(elbv2ManagedByTag), Value: aws.String(elbv2ManagedByValue)},
							{Key: aws.String(elbv2LBTag), Value: aws.String(lbArn)},
						},
					},
				},
			}, accountID)
			if eniErr != nil {
				// Rollback: delete any ENIs already created
				for _, rollbackENI := range eniIDs {
					s.vpcService.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
						NetworkInterfaceId: aws.String(rollbackENI),
					}, accountID)
				}
				slog.Error("CreateLoadBalancer: failed to create ENI", "subnet", subnetID, "err", eniErr)
				return nil, errors.New(awserrors.ErrorELBv2SubnetNotFound)
			}

			eni := eniOut.NetworkInterface
			eniIDs = append(eniIDs, *eni.NetworkInterfaceId)

			if eni.VpcId != nil && vpcID == "" {
				vpcID = *eni.VpcId
			}
			az := ""
			if eni.AvailabilityZone != nil {
				az = *eni.AvailabilityZone
			}
			availZones = append(availZones, AvailZoneInfo{
				ZoneName: az,
				SubnetId: subnetID,
			})
		}
	}

	record := &LoadBalancerRecord{
		LoadBalancerArn: lbArn,
		LoadBalancerID:  lbID,
		DNSName:         dnsName,
		Name:            name,
		Scheme:          scheme,
		Type:            LoadBalancerTypeApplication,
		State:           StateActive,
		VpcId:           vpcID,
		SecurityGroups:  securityGroups,
		Subnets:         subnets,
		AvailZones:      availZones,
		ENIs:            eniIDs,
		IPAddressType:   IPAddressTypeIPv4,
		NodeID:          s.nodeID,
		Tags:            tags,
		AccountID:       accountID,
		CreatedAt:       time.Now().UTC(),
	}

	if err := s.store.PutLoadBalancer(record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreateLoadBalancer completed", "name", name, "lbArn", lbArn, "enis", len(eniIDs), "accountID", accountID)

	return &elbv2.CreateLoadBalancerOutput{
		LoadBalancers: []*elbv2.LoadBalancer{s.lbRecordToSDK(record)},
	}, nil
}

func (s *ELBv2ServiceImpl) DeleteLoadBalancer(input *elbv2.DeleteLoadBalancerInput, accountID string) (*elbv2.DeleteLoadBalancerOutput, error) {
	if input.LoadBalancerArn == nil || *input.LoadBalancerArn == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	lb, err := s.store.GetLoadBalancerByArn(*input.LoadBalancerArn)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if lb == nil {
		return nil, errors.New(awserrors.ErrorELBv2LoadBalancerNotFound)
	}

	// Delete all listeners for this LB
	listeners, err := s.store.ListListenersByLB(lb.LoadBalancerArn)
	if err != nil {
		slog.Warn("Failed to list listeners for cleanup", "lbArn", lb.LoadBalancerArn, "err", err)
	}
	for _, l := range listeners {
		if err := s.store.DeleteListener(l.ListenerID); err != nil {
			slog.Warn("Failed to delete listener during LB cleanup", "listenerID", l.ListenerID, "err", err)
		}
	}

	// Delete system-managed ENIs
	if s.vpcService != nil {
		for _, eniID := range lb.ENIs {
			_, eniErr := s.vpcService.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
				NetworkInterfaceId: aws.String(eniID),
			}, accountID)
			if eniErr != nil {
				slog.Warn("Failed to delete ALB ENI during cleanup", "eniId", eniID, "err", eniErr)
			}
		}
	}

	if err := s.store.DeleteLoadBalancer(lb.LoadBalancerID); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeleteLoadBalancer completed", "lbArn", *input.LoadBalancerArn, "enis", len(lb.ENIs), "accountID", accountID)

	return &elbv2.DeleteLoadBalancerOutput{}, nil
}

func (s *ELBv2ServiceImpl) DescribeLoadBalancers(input *elbv2.DescribeLoadBalancersInput, accountID string) (*elbv2.DescribeLoadBalancersOutput, error) {
	allLBs, err := s.store.ListLoadBalancers()
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Build filter sets
	arnFilter := make(map[string]bool)
	for _, arn := range input.LoadBalancerArns {
		if arn != nil {
			arnFilter[*arn] = true
		}
	}
	nameFilter := make(map[string]bool)
	for _, name := range input.Names {
		if name != nil {
			nameFilter[*name] = true
		}
	}

	var result []*elbv2.LoadBalancer
	for _, lb := range allLBs {
		if lb.AccountID != accountID {
			continue
		}
		if len(arnFilter) > 0 && !arnFilter[lb.LoadBalancerArn] {
			continue
		}
		if len(nameFilter) > 0 && !nameFilter[lb.Name] {
			continue
		}
		result = append(result, s.lbRecordToSDK(lb))
	}

	return &elbv2.DescribeLoadBalancersOutput{
		LoadBalancers: result,
	}, nil
}

// --- Target Group operations ---

func (s *ELBv2ServiceImpl) CreateTargetGroup(input *elbv2.CreateTargetGroupInput, accountID string) (*elbv2.CreateTargetGroupOutput, error) {
	if input.Name == nil || *input.Name == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	name := *input.Name

	protocol := ProtocolHTTP
	if input.Protocol != nil && *input.Protocol != "" {
		protocol = *input.Protocol
	}

	port := int64(80)
	if input.Port != nil {
		port = *input.Port
	}

	vpcID := ""
	if input.VpcId != nil {
		vpcID = *input.VpcId
	}

	// Check duplicate name within VPC
	existing, err := s.store.GetTargetGroupByName(name, vpcID)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if existing != nil {
		return nil, errors.New(awserrors.ErrorELBv2DuplicateTargetGroup)
	}

	targetType := "instance"
	if input.TargetType != nil && *input.TargetType != "" {
		targetType = *input.TargetType
	}

	hc := DefaultHealthCheck()
	if input.HealthCheckProtocol != nil {
		hc.Protocol = *input.HealthCheckProtocol
	}
	if input.HealthCheckPort != nil {
		hc.Port = *input.HealthCheckPort
	}
	if input.HealthCheckPath != nil {
		hc.Path = *input.HealthCheckPath
	}
	if input.HealthCheckIntervalSeconds != nil {
		hc.IntervalSeconds = *input.HealthCheckIntervalSeconds
	}
	if input.HealthCheckTimeoutSeconds != nil {
		hc.TimeoutSeconds = *input.HealthCheckTimeoutSeconds
	}
	if input.HealthyThresholdCount != nil {
		hc.HealthyThreshold = *input.HealthyThresholdCount
	}
	if input.UnhealthyThresholdCount != nil {
		hc.UnhealthyThreshold = *input.UnhealthyThresholdCount
	}
	if input.Matcher != nil && input.Matcher.HttpCode != nil {
		hc.Matcher = *input.Matcher.HttpCode
	}

	tgID := utils.GenerateResourceID("tg")
	tgArn := buildTGArn(s.region, accountID, name, tgID)

	tags := make(map[string]string)
	for _, tag := range input.Tags {
		if tag.Key != nil && tag.Value != nil {
			tags[*tag.Key] = *tag.Value
		}
	}

	record := &TargetGroupRecord{
		TargetGroupArn: tgArn,
		TargetGroupID:  tgID,
		Name:           name,
		Protocol:       protocol,
		Port:           port,
		VpcId:          vpcID,
		TargetType:     targetType,
		HealthCheck:    hc,
		Tags:           tags,
		AccountID:      accountID,
		CreatedAt:      time.Now().UTC(),
	}

	if err := s.store.PutTargetGroup(record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreateTargetGroup completed", "name", name, "tgArn", tgArn, "accountID", accountID)

	return &elbv2.CreateTargetGroupOutput{
		TargetGroups: []*elbv2.TargetGroup{s.tgRecordToSDK(record)},
	}, nil
}

func (s *ELBv2ServiceImpl) DeleteTargetGroup(input *elbv2.DeleteTargetGroupInput, accountID string) (*elbv2.DeleteTargetGroupOutput, error) {
	if input.TargetGroupArn == nil || *input.TargetGroupArn == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	tg, err := s.store.GetTargetGroupByArn(*input.TargetGroupArn)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if tg == nil {
		return nil, errors.New(awserrors.ErrorELBv2TargetGroupNotFound)
	}

	// Check if any listener references this target group
	listeners, err := s.store.ListListeners()
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	for _, l := range listeners {
		for _, action := range l.DefaultActions {
			if action.TargetGroupArn == tg.TargetGroupArn {
				return nil, errors.New(awserrors.ErrorELBv2TargetGroupInUse)
			}
		}
	}

	if err := s.store.DeleteTargetGroup(tg.TargetGroupID); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeleteTargetGroup completed", "tgArn", *input.TargetGroupArn, "accountID", accountID)

	return &elbv2.DeleteTargetGroupOutput{}, nil
}

func (s *ELBv2ServiceImpl) DescribeTargetGroups(input *elbv2.DescribeTargetGroupsInput, accountID string) (*elbv2.DescribeTargetGroupsOutput, error) {
	allTGs, err := s.store.ListTargetGroups()
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	arnFilter := make(map[string]bool)
	for _, arn := range input.TargetGroupArns {
		if arn != nil {
			arnFilter[*arn] = true
		}
	}
	nameFilter := make(map[string]bool)
	for _, name := range input.Names {
		if name != nil {
			nameFilter[*name] = true
		}
	}

	var result []*elbv2.TargetGroup
	for _, tg := range allTGs {
		if tg.AccountID != accountID {
			continue
		}
		if len(arnFilter) > 0 && !arnFilter[tg.TargetGroupArn] {
			continue
		}
		if len(nameFilter) > 0 && !nameFilter[tg.Name] {
			continue
		}
		// Filter by LB ARN if specified
		if input.LoadBalancerArn != nil && *input.LoadBalancerArn != "" {
			// Check if any listener on this LB references this TG
			listeners, _ := s.store.ListListenersByLB(*input.LoadBalancerArn)
			found := false
			for _, l := range listeners {
				for _, a := range l.DefaultActions {
					if a.TargetGroupArn == tg.TargetGroupArn {
						found = true
					}
				}
			}
			if !found {
				continue
			}
		}
		result = append(result, s.tgRecordToSDK(tg))
	}

	return &elbv2.DescribeTargetGroupsOutput{
		TargetGroups: result,
	}, nil
}

// --- Target registration ---

func (s *ELBv2ServiceImpl) RegisterTargets(input *elbv2.RegisterTargetsInput, accountID string) (*elbv2.RegisterTargetsOutput, error) {
	if input.TargetGroupArn == nil || *input.TargetGroupArn == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	tg, err := s.store.GetTargetGroupByArn(*input.TargetGroupArn)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if tg == nil {
		return nil, errors.New(awserrors.ErrorELBv2TargetGroupNotFound)
	}

	// Build map of existing targets for dedup
	existing := make(map[string]int) // id:port -> index
	for i, t := range tg.Targets {
		key := fmt.Sprintf("%s:%d", t.Id, t.Port)
		existing[key] = i
	}

	for _, td := range input.Targets {
		if td.Id == nil {
			continue
		}
		port := tg.Port
		if td.Port != nil {
			port = *td.Port
		}
		key := fmt.Sprintf("%s:%d", *td.Id, port)
		if _, exists := existing[key]; exists {
			continue // Already registered
		}
		tg.Targets = append(tg.Targets, Target{
			Id:          *td.Id,
			Port:        port,
			HealthState: TargetHealthInitial,
			HealthDesc:  "Target registration is in progress",
		})
	}

	if err := s.store.PutTargetGroup(tg); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("RegisterTargets completed", "tgArn", *input.TargetGroupArn, "targetsAdded", len(input.Targets), "accountID", accountID)

	return &elbv2.RegisterTargetsOutput{}, nil
}

func (s *ELBv2ServiceImpl) DeregisterTargets(input *elbv2.DeregisterTargetsInput, accountID string) (*elbv2.DeregisterTargetsOutput, error) {
	if input.TargetGroupArn == nil || *input.TargetGroupArn == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	tg, err := s.store.GetTargetGroupByArn(*input.TargetGroupArn)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if tg == nil {
		return nil, errors.New(awserrors.ErrorELBv2TargetGroupNotFound)
	}

	// Build removal set
	removeSet := make(map[string]bool)
	for _, td := range input.Targets {
		if td.Id == nil {
			continue
		}
		port := tg.Port
		if td.Port != nil {
			port = *td.Port
		}
		removeSet[fmt.Sprintf("%s:%d", *td.Id, port)] = true
	}

	var remaining []Target
	for _, t := range tg.Targets {
		key := fmt.Sprintf("%s:%d", t.Id, t.Port)
		if !removeSet[key] {
			remaining = append(remaining, t)
		}
	}
	tg.Targets = remaining

	if err := s.store.PutTargetGroup(tg); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeregisterTargets completed", "tgArn", *input.TargetGroupArn, "targetsRemoved", len(input.Targets), "accountID", accountID)

	return &elbv2.DeregisterTargetsOutput{}, nil
}

func (s *ELBv2ServiceImpl) DescribeTargetHealth(input *elbv2.DescribeTargetHealthInput, accountID string) (*elbv2.DescribeTargetHealthOutput, error) {
	if input.TargetGroupArn == nil || *input.TargetGroupArn == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	tg, err := s.store.GetTargetGroupByArn(*input.TargetGroupArn)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if tg == nil {
		return nil, errors.New(awserrors.ErrorELBv2TargetGroupNotFound)
	}

	// Optional: filter to specific targets
	targetFilter := make(map[string]bool)
	for _, td := range input.Targets {
		if td.Id != nil {
			targetFilter[*td.Id] = true
		}
	}

	var descriptions []*elbv2.TargetHealthDescription
	for _, t := range tg.Targets {
		if len(targetFilter) > 0 && !targetFilter[t.Id] {
			continue
		}

		desc := &elbv2.TargetHealthDescription{
			Target: &elbv2.TargetDescription{
				Id:   aws.String(t.Id),
				Port: aws.Int64(t.Port),
			},
			TargetHealth: &elbv2.TargetHealth{
				State:       aws.String(t.HealthState),
				Description: aws.String(t.HealthDesc),
			},
		}
		descriptions = append(descriptions, desc)
	}

	return &elbv2.DescribeTargetHealthOutput{
		TargetHealthDescriptions: descriptions,
	}, nil
}

// --- Listener operations ---

func (s *ELBv2ServiceImpl) CreateListener(input *elbv2.CreateListenerInput, accountID string) (*elbv2.CreateListenerOutput, error) {
	if input.LoadBalancerArn == nil || *input.LoadBalancerArn == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if len(input.DefaultActions) == 0 {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	lb, err := s.store.GetLoadBalancerByArn(*input.LoadBalancerArn)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if lb == nil {
		return nil, errors.New(awserrors.ErrorELBv2LoadBalancerNotFound)
	}

	protocol := ProtocolHTTP
	if input.Protocol != nil && *input.Protocol != "" {
		protocol = *input.Protocol
	}

	port := int64(80)
	if input.Port != nil {
		port = *input.Port
	}

	// Check for duplicate listener on same port
	existingListeners, err := s.store.ListListenersByLB(lb.LoadBalancerArn)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	for _, l := range existingListeners {
		if l.Port == port {
			return nil, errors.New(awserrors.ErrorELBv2DuplicateListener)
		}
	}

	listenerID := utils.GenerateResourceID("lst")
	listenerArn := buildListenerArn(s.region, accountID, lb.Name, lb.LoadBalancerID, listenerID)

	var actions []ListenerAction
	for _, a := range input.DefaultActions {
		action := ListenerAction{}
		if a.Type != nil {
			action.Type = *a.Type
		}
		if a.TargetGroupArn != nil {
			action.TargetGroupArn = *a.TargetGroupArn
		}
		actions = append(actions, action)
	}

	record := &ListenerRecord{
		ListenerArn:     listenerArn,
		ListenerID:      listenerID,
		LoadBalancerArn: lb.LoadBalancerArn,
		Protocol:        protocol,
		Port:            port,
		DefaultActions:  actions,
		AccountID:       accountID,
		CreatedAt:       time.Now().UTC(),
	}

	if err := s.store.PutListener(record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreateListener completed", "listenerArn", listenerArn, "lbArn", lb.LoadBalancerArn, "port", port, "accountID", accountID)

	return &elbv2.CreateListenerOutput{
		Listeners: []*elbv2.Listener{s.listenerRecordToSDK(record)},
	}, nil
}

func (s *ELBv2ServiceImpl) DeleteListener(input *elbv2.DeleteListenerInput, accountID string) (*elbv2.DeleteListenerOutput, error) {
	if input.ListenerArn == nil || *input.ListenerArn == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	listener, err := s.store.GetListenerByArn(*input.ListenerArn)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if listener == nil {
		return nil, errors.New(awserrors.ErrorELBv2ListenerNotFound)
	}

	if err := s.store.DeleteListener(listener.ListenerID); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeleteListener completed", "listenerArn", *input.ListenerArn, "accountID", accountID)

	return &elbv2.DeleteListenerOutput{}, nil
}

func (s *ELBv2ServiceImpl) DescribeListeners(input *elbv2.DescribeListenersInput, accountID string) (*elbv2.DescribeListenersOutput, error) {
	var listeners []*ListenerRecord
	var err error

	if input.LoadBalancerArn != nil && *input.LoadBalancerArn != "" {
		listeners, err = s.store.ListListenersByLB(*input.LoadBalancerArn)
	} else {
		listeners, err = s.store.ListListeners()
	}
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Filter by ARN if specified
	arnFilter := make(map[string]bool)
	for _, arn := range input.ListenerArns {
		if arn != nil {
			arnFilter[*arn] = true
		}
	}

	var result []*elbv2.Listener
	for _, l := range listeners {
		if l.AccountID != accountID {
			continue
		}
		if len(arnFilter) > 0 && !arnFilter[l.ListenerArn] {
			continue
		}
		result = append(result, s.listenerRecordToSDK(l))
	}

	return &elbv2.DescribeListenersOutput{
		Listeners: result,
	}, nil
}

// --- SDK type conversion helpers ---

func (s *ELBv2ServiceImpl) lbRecordToSDK(r *LoadBalancerRecord) *elbv2.LoadBalancer {
	lb := &elbv2.LoadBalancer{
		LoadBalancerArn: aws.String(r.LoadBalancerArn),
		LoadBalancerName: aws.String(r.Name),
		DNSName:         aws.String(r.DNSName),
		Scheme:          aws.String(r.Scheme),
		Type:            aws.String(r.Type),
		IpAddressType:   aws.String(r.IPAddressType),
		CreatedTime:     aws.Time(r.CreatedAt),
		VpcId:           aws.String(r.VpcId),
		State: &elbv2.LoadBalancerState{
			Code: aws.String(r.State),
		},
	}

	for _, sg := range r.SecurityGroups {
		lb.SecurityGroups = append(lb.SecurityGroups, aws.String(sg))
	}

	for _, az := range r.AvailZones {
		lb.AvailabilityZones = append(lb.AvailabilityZones, &elbv2.AvailabilityZone{
			ZoneName: aws.String(az.ZoneName),
			SubnetId: aws.String(az.SubnetId),
		})
	}

	return lb
}

func (s *ELBv2ServiceImpl) tgRecordToSDK(r *TargetGroupRecord) *elbv2.TargetGroup {
	return &elbv2.TargetGroup{
		TargetGroupArn:         aws.String(r.TargetGroupArn),
		TargetGroupName:        aws.String(r.Name),
		Protocol:               aws.String(r.Protocol),
		Port:                   aws.Int64(r.Port),
		VpcId:                  aws.String(r.VpcId),
		TargetType:             aws.String(r.TargetType),
		HealthCheckProtocol:    aws.String(r.HealthCheck.Protocol),
		HealthCheckPort:        aws.String(r.HealthCheck.Port),
		HealthCheckPath:        aws.String(r.HealthCheck.Path),
		HealthCheckIntervalSeconds: aws.Int64(r.HealthCheck.IntervalSeconds),
		HealthCheckTimeoutSeconds:  aws.Int64(r.HealthCheck.TimeoutSeconds),
		HealthyThresholdCount:     aws.Int64(r.HealthCheck.HealthyThreshold),
		UnhealthyThresholdCount:   aws.Int64(r.HealthCheck.UnhealthyThreshold),
		Matcher: &elbv2.Matcher{
			HttpCode: aws.String(r.HealthCheck.Matcher),
		},
	}
}

func (s *ELBv2ServiceImpl) listenerRecordToSDK(r *ListenerRecord) *elbv2.Listener {
	listener := &elbv2.Listener{
		ListenerArn:     aws.String(r.ListenerArn),
		LoadBalancerArn: aws.String(r.LoadBalancerArn),
		Protocol:        aws.String(r.Protocol),
		Port:            aws.Int64(r.Port),
	}

	for _, a := range r.DefaultActions {
		action := &elbv2.Action{
			Type:           aws.String(a.Type),
			TargetGroupArn: aws.String(a.TargetGroupArn),
		}
		listener.DefaultActions = append(listener.DefaultActions, action)
	}

	return listener
}
