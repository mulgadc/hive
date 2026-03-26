package handlers_ec2_routetable

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/config"
	handlers_ec2_igw "github.com/mulgadc/spinifex/spinifex/handlers/ec2/igw"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

// Ensure RouteTableServiceImpl implements RouteTableService
var _ RouteTableService = (*RouteTableServiceImpl)(nil)

// RouteTableServiceImpl implements Route Table operations with NATS JetStream persistence
type RouteTableServiceImpl struct {
	config   *config.Config
	rtbKV    nats.KeyValue
	vpcKV    nats.KeyValue
	igwKV    nats.KeyValue
	subnetKV nats.KeyValue
	natsConn *nats.Conn
}

// NewRouteTableServiceImplWithNATS creates a Route Table service with NATS JetStream for persistence
func NewRouteTableServiceImplWithNATS(cfg *config.Config, natsConn *nats.Conn) (*RouteTableServiceImpl, error) {
	js, err := natsConn.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	rtbKV, err := utils.GetOrCreateKVBucket(js, KVBucketRouteTables, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to create KV bucket %s: %w", KVBucketRouteTables, err)
	}
	if err := utils.WriteVersion(rtbKV, KVBucketRouteTablesVersion); err != nil {
		return nil, fmt.Errorf("write version to %s: %w", KVBucketRouteTables, err)
	}

	vpcKV, err := utils.GetOrCreateKVBucket(js, handlers_ec2_vpc.KVBucketVPCs, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to get VPC KV bucket: %w", err)
	}

	igwKV, err := utils.GetOrCreateKVBucket(js, handlers_ec2_igw.KVBucketIGW, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to get IGW KV bucket: %w", err)
	}

	subnetKV, err := utils.GetOrCreateKVBucket(js, handlers_ec2_vpc.KVBucketSubnets, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to get subnet KV bucket: %w", err)
	}

	slog.Info("RouteTable service initialized with JetStream KV", "bucket", KVBucketRouteTables)

	return &RouteTableServiceImpl{
		config:   cfg,
		rtbKV:    rtbKV,
		vpcKV:    vpcKV,
		igwKV:    igwKV,
		subnetKV: subnetKV,
		natsConn: natsConn,
	}, nil
}

// getRouteTable retrieves a route table record from KV
func (s *RouteTableServiceImpl) getRouteTable(accountID, rtbID string) (*RouteTableRecord, error) {
	entry, err := s.rtbKV.Get(utils.AccountKey(accountID, rtbID))
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidRouteTableIDNotFound)
	}
	var record RouteTableRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	return &record, nil
}

// putRouteTable stores a route table record to KV
func (s *RouteTableServiceImpl) putRouteTable(accountID string, record *RouteTableRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return errors.New(awserrors.ErrorServerInternal)
	}
	if _, err := s.rtbKV.Put(utils.AccountKey(accountID, record.RouteTableId), data); err != nil {
		return errors.New(awserrors.ErrorServerInternal)
	}
	return nil
}

// getVPCCidr looks up a VPC's CIDR block from the VPC KV bucket
func (s *RouteTableServiceImpl) getVPCCidr(accountID, vpcID string) (string, error) {
	entry, err := s.vpcKV.Get(utils.AccountKey(accountID, vpcID))
	if err != nil {
		return "", errors.New(awserrors.ErrorInvalidVpcIDNotFound)
	}
	var vpcRecord handlers_ec2_vpc.VPCRecord
	if err := json.Unmarshal(entry.Value(), &vpcRecord); err != nil {
		return "", errors.New(awserrors.ErrorServerInternal)
	}
	return vpcRecord.CidrBlock, nil
}

// allRouteTablesForVPC returns all route tables belonging to a VPC
func (s *RouteTableServiceImpl) allRouteTablesForVPC(accountID, vpcID string) ([]RouteTableRecord, error) {
	prefix := accountID + "."
	keys, err := s.rtbKV.Keys()
	if err != nil {
		if errors.Is(err, nats.ErrNoKeysFound) {
			return nil, nil
		}
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	var results []RouteTableRecord
	for _, key := range keys {
		if key == utils.VersionKey || !strings.HasPrefix(key, prefix) {
			continue
		}
		entry, err := s.rtbKV.Get(key)
		if err != nil {
			continue
		}
		var record RouteTableRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			continue
		}
		if record.VpcId == vpcID {
			results = append(results, record)
		}
	}
	return results, nil
}

// CreateRouteTableForVPC creates a route table with the local route pre-populated.
// Exported for use by VPC service when creating default VPCs.
func (s *RouteTableServiceImpl) CreateRouteTableForVPC(vpcID, vpcCidr, accountID string, isMain bool, rtbID string) (*RouteTableRecord, error) {
	if rtbID == "" {
		rtbID = utils.GenerateResourceID("rtb")
	}

	record := RouteTableRecord{
		RouteTableId: rtbID,
		VpcId:        vpcID,
		AccountID:    accountID,
		IsMain:       isMain,
		Routes: []RouteRecord{
			{
				DestinationCidrBlock: vpcCidr,
				GatewayId:            "local",
				State:                "active",
				Origin:               "CreateRouteTable",
			},
		},
		Tags:      make(map[string]string),
		CreatedAt: time.Now(),
	}

	if isMain {
		record.Associations = []AssociationRecord{
			{
				AssociationId: utils.GenerateResourceID("rtbassoc"),
				Main:          true,
			},
		}
	}

	if err := s.putRouteTable(accountID, &record); err != nil {
		return nil, err
	}

	slog.Info("Created route table", "routeTableId", rtbID, "vpcId", vpcID, "isMain", isMain, "accountID", accountID)
	return &record, nil
}

// CreateRouteTable creates a new custom (non-main) route table for a VPC
func (s *RouteTableServiceImpl) CreateRouteTable(input *ec2.CreateRouteTableInput, accountID string) (*ec2.CreateRouteTableOutput, error) {
	if input.VpcId == nil || *input.VpcId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	vpcID := *input.VpcId
	vpcCidr, err := s.getVPCCidr(accountID, vpcID)
	if err != nil {
		return nil, err
	}

	record, err := s.CreateRouteTableForVPC(vpcID, vpcCidr, accountID, false, "")
	if err != nil {
		return nil, err
	}

	return &ec2.CreateRouteTableOutput{
		RouteTable: recordToEC2(record),
	}, nil
}

// DeleteRouteTable deletes a route table (must not be main, must have no subnet associations)
func (s *RouteTableServiceImpl) DeleteRouteTable(input *ec2.DeleteRouteTableInput, accountID string) (*ec2.DeleteRouteTableOutput, error) {
	if input.RouteTableId == nil || *input.RouteTableId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	rtbID := *input.RouteTableId
	record, err := s.getRouteTable(accountID, rtbID)
	if err != nil {
		return nil, err
	}

	if record.IsMain {
		return nil, errors.New(awserrors.ErrorDependencyViolation)
	}

	// Check for non-main associations (subnets still using this table)
	for _, assoc := range record.Associations {
		if !assoc.Main && assoc.SubnetId != "" {
			return nil, errors.New(awserrors.ErrorDependencyViolation)
		}
	}

	if err := s.rtbKV.Delete(utils.AccountKey(accountID, rtbID)); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeleteRouteTable completed", "routeTableId", rtbID, "accountID", accountID)
	return &ec2.DeleteRouteTableOutput{}, nil
}

// DescribeRouteTables lists route tables, optionally filtered
func (s *RouteTableServiceImpl) DescribeRouteTables(input *ec2.DescribeRouteTablesInput, accountID string) (*ec2.DescribeRouteTablesOutput, error) {
	rtbIDs := make(map[string]bool)
	for _, id := range input.RouteTableIds {
		if id != nil {
			rtbIDs[*id] = true
		}
	}

	// Build filter map
	filters := make(map[string][]string)
	for _, f := range input.Filters {
		if f.Name != nil {
			for _, v := range f.Values {
				if v != nil {
					filters[*f.Name] = append(filters[*f.Name], *v)
				}
			}
		}
	}

	prefix := accountID + "."
	keys, err := s.rtbKV.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	var routeTables []*ec2.RouteTable
	foundIDs := make(map[string]bool)

	for _, key := range keys {
		if key == utils.VersionKey || !strings.HasPrefix(key, prefix) {
			continue
		}

		entry, err := s.rtbKV.Get(key)
		if err != nil {
			continue
		}

		var record RouteTableRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			continue
		}

		if len(rtbIDs) > 0 && !rtbIDs[record.RouteTableId] {
			continue
		}

		if !matchesFilters(&record, filters) {
			continue
		}

		routeTables = append(routeTables, recordToEC2(&record))
		foundIDs[record.RouteTableId] = true
	}

	// Return error if specific IDs were requested but not found
	for id := range rtbIDs {
		if !foundIDs[id] {
			return nil, errors.New(awserrors.ErrorInvalidRouteTableIDNotFound)
		}
	}

	return &ec2.DescribeRouteTablesOutput{
		RouteTables: routeTables,
	}, nil
}

// CreateRoute adds a route to a route table
func (s *RouteTableServiceImpl) CreateRoute(input *ec2.CreateRouteInput, accountID string) (*ec2.CreateRouteOutput, error) {
	if input.RouteTableId == nil || *input.RouteTableId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.DestinationCidrBlock == nil || *input.DestinationCidrBlock == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	rtbID := *input.RouteTableId
	destCidr := *input.DestinationCidrBlock

	record, err := s.getRouteTable(accountID, rtbID)
	if err != nil {
		return nil, err
	}

	// Check for duplicate destination
	for _, r := range record.Routes {
		if r.DestinationCidrBlock == destCidr {
			return nil, errors.New(awserrors.ErrorRouteAlreadyExists)
		}
	}

	// V1: only GatewayId target supported
	if input.GatewayId == nil || *input.GatewayId == "" {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	igwID := *input.GatewayId

	// Verify IGW exists and is attached to the same VPC
	igwEntry, err := s.igwKV.Get(utils.AccountKey(accountID, igwID))
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidInternetGatewayIDNotFound)
	}
	var igwRecord handlers_ec2_igw.IGWRecord
	if err := json.Unmarshal(igwEntry.Value(), &igwRecord); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if igwRecord.VpcId != record.VpcId {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	route := RouteRecord{
		DestinationCidrBlock: destCidr,
		GatewayId:            igwID,
		State:                "active",
		Origin:               "CreateRoute",
	}
	record.Routes = append(record.Routes, route)

	if err := s.putRouteTable(accountID, record); err != nil {
		return nil, err
	}

	slog.Info("CreateRoute completed", "routeTableId", rtbID, "destination", destCidr, "gatewayId", igwID, "accountID", accountID)

	return &ec2.CreateRouteOutput{
		Return: aws.Bool(true),
	}, nil
}

// DeleteRoute removes a route from a route table (cannot delete local route)
func (s *RouteTableServiceImpl) DeleteRoute(input *ec2.DeleteRouteInput, accountID string) (*ec2.DeleteRouteOutput, error) {
	if input.RouteTableId == nil || *input.RouteTableId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.DestinationCidrBlock == nil || *input.DestinationCidrBlock == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	rtbID := *input.RouteTableId
	destCidr := *input.DestinationCidrBlock

	record, err := s.getRouteTable(accountID, rtbID)
	if err != nil {
		return nil, err
	}

	idx := -1
	for i, r := range record.Routes {
		if r.DestinationCidrBlock == destCidr {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, errors.New(awserrors.ErrorInvalidRouteNotFound)
	}

	// Cannot delete local route
	if record.Routes[idx].GatewayId == "local" {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	record.Routes = append(record.Routes[:idx], record.Routes[idx+1:]...)

	if err := s.putRouteTable(accountID, record); err != nil {
		return nil, err
	}

	slog.Info("DeleteRoute completed", "routeTableId", rtbID, "destination", destCidr, "accountID", accountID)

	return &ec2.DeleteRouteOutput{}, nil
}

// ReplaceRoute atomically replaces the target of an existing route
func (s *RouteTableServiceImpl) ReplaceRoute(input *ec2.ReplaceRouteInput, accountID string) (*ec2.ReplaceRouteOutput, error) {
	if input.RouteTableId == nil || *input.RouteTableId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.DestinationCidrBlock == nil || *input.DestinationCidrBlock == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	rtbID := *input.RouteTableId
	destCidr := *input.DestinationCidrBlock

	record, err := s.getRouteTable(accountID, rtbID)
	if err != nil {
		return nil, err
	}

	idx := -1
	for i, r := range record.Routes {
		if r.DestinationCidrBlock == destCidr {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, errors.New(awserrors.ErrorInvalidRouteNotFound)
	}

	if record.Routes[idx].GatewayId == "local" {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	// V1: only GatewayId target supported
	if input.GatewayId == nil || *input.GatewayId == "" {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	igwID := *input.GatewayId

	// Verify IGW exists and is attached to same VPC
	igwEntry, err := s.igwKV.Get(utils.AccountKey(accountID, igwID))
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidInternetGatewayIDNotFound)
	}
	var igwRecord handlers_ec2_igw.IGWRecord
	if err := json.Unmarshal(igwEntry.Value(), &igwRecord); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if igwRecord.VpcId != record.VpcId {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	record.Routes[idx].GatewayId = igwID
	record.Routes[idx].NatGatewayId = ""
	record.Routes[idx].State = "active"

	if err := s.putRouteTable(accountID, record); err != nil {
		return nil, err
	}

	slog.Info("ReplaceRoute completed", "routeTableId", rtbID, "destination", destCidr, "gatewayId", igwID, "accountID", accountID)

	return &ec2.ReplaceRouteOutput{}, nil
}

// AssociateRouteTable associates a subnet with a route table
func (s *RouteTableServiceImpl) AssociateRouteTable(input *ec2.AssociateRouteTableInput, accountID string) (*ec2.AssociateRouteTableOutput, error) {
	if input.RouteTableId == nil || *input.RouteTableId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.SubnetId == nil || *input.SubnetId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	rtbID := *input.RouteTableId
	subnetID := *input.SubnetId

	record, err := s.getRouteTable(accountID, rtbID)
	if err != nil {
		return nil, err
	}

	// Verify subnet exists and belongs to the same VPC
	subnetEntry, err := s.subnetKV.Get(utils.AccountKey(accountID, subnetID))
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidSubnetIDNotFound)
	}
	var subnetRecord handlers_ec2_vpc.SubnetRecord
	if err := json.Unmarshal(subnetEntry.Value(), &subnetRecord); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if subnetRecord.VpcId != record.VpcId {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	// Check the subnet doesn't already have an explicit association in any route table
	allTables, err := s.allRouteTablesForVPC(accountID, record.VpcId)
	if err != nil {
		return nil, err
	}
	for _, table := range allTables {
		for _, assoc := range table.Associations {
			if assoc.SubnetId == subnetID && !assoc.Main {
				return nil, errors.New(awserrors.ErrorResourceAlreadyAssociated)
			}
		}
	}

	assocID := utils.GenerateResourceID("rtbassoc")
	record.Associations = append(record.Associations, AssociationRecord{
		AssociationId: assocID,
		SubnetId:      subnetID,
		Main:          false,
	})

	if err := s.putRouteTable(accountID, record); err != nil {
		return nil, err
	}

	slog.Info("AssociateRouteTable completed", "routeTableId", rtbID, "subnetId", subnetID, "associationId", assocID, "accountID", accountID)

	return &ec2.AssociateRouteTableOutput{
		AssociationId: aws.String(assocID),
		AssociationState: &ec2.RouteTableAssociationState{
			State: aws.String("associated"),
		},
	}, nil
}

// DisassociateRouteTable removes a subnet association (cannot disassociate main)
func (s *RouteTableServiceImpl) DisassociateRouteTable(input *ec2.DisassociateRouteTableInput, accountID string) (*ec2.DisassociateRouteTableOutput, error) {
	if input.AssociationId == nil || *input.AssociationId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	assocID := *input.AssociationId

	// Search all route tables for this account to find the association
	prefix := accountID + "."
	keys, err := s.rtbKV.Keys()
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidAssociationIDNotFound)
	}

	for _, key := range keys {
		if key == utils.VersionKey || !strings.HasPrefix(key, prefix) {
			continue
		}

		entry, err := s.rtbKV.Get(key)
		if err != nil {
			continue
		}

		var record RouteTableRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			continue
		}

		for i, assoc := range record.Associations {
			if assoc.AssociationId == assocID {
				if assoc.Main {
					return nil, errors.New(awserrors.ErrorInvalidParameterValue)
				}
				record.Associations = append(record.Associations[:i], record.Associations[i+1:]...)
				if err := s.putRouteTable(accountID, &record); err != nil {
					return nil, err
				}

				slog.Info("DisassociateRouteTable completed", "associationId", assocID, "routeTableId", record.RouteTableId, "accountID", accountID)
				return &ec2.DisassociateRouteTableOutput{}, nil
			}
		}
	}

	return nil, errors.New(awserrors.ErrorInvalidAssociationIDNotFound)
}

// ReplaceRouteTableAssociation atomically moves a subnet from one route table to another
func (s *RouteTableServiceImpl) ReplaceRouteTableAssociation(input *ec2.ReplaceRouteTableAssociationInput, accountID string) (*ec2.ReplaceRouteTableAssociationOutput, error) {
	if input.AssociationId == nil || *input.AssociationId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}
	if input.RouteTableId == nil || *input.RouteTableId == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	assocID := *input.AssociationId
	newRtbID := *input.RouteTableId

	// Verify the new route table exists
	newRecord, err := s.getRouteTable(accountID, newRtbID)
	if err != nil {
		return nil, err
	}

	// Find and remove the association from its current route table
	prefix := accountID + "."
	keys, err := s.rtbKV.Keys()
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidAssociationIDNotFound)
	}

	for _, key := range keys {
		if key == utils.VersionKey || !strings.HasPrefix(key, prefix) {
			continue
		}

		entry, err := s.rtbKV.Get(key)
		if err != nil {
			continue
		}

		var oldRecord RouteTableRecord
		if err := json.Unmarshal(entry.Value(), &oldRecord); err != nil {
			continue
		}

		for i, assoc := range oldRecord.Associations {
			if assoc.AssociationId != assocID {
				continue
			}

			// If replacing a main association, swap main table for the VPC
			if assoc.Main {
				// New table becomes main, old table loses main status
				newRecord.IsMain = true
				oldRecord.IsMain = false
			}

			// Remove from old table
			oldRecord.Associations = append(oldRecord.Associations[:i], oldRecord.Associations[i+1:]...)
			if err := s.putRouteTable(accountID, &oldRecord); err != nil {
				return nil, err
			}

			// Add to new table with new ID
			newAssocID := utils.GenerateResourceID("rtbassoc")
			newRecord.Associations = append(newRecord.Associations, AssociationRecord{
				AssociationId: newAssocID,
				SubnetId:      assoc.SubnetId,
				Main:          assoc.Main,
			})
			if err := s.putRouteTable(accountID, newRecord); err != nil {
				return nil, err
			}

			slog.Info("ReplaceRouteTableAssociation completed",
				"oldAssociationId", assocID, "newAssociationId", newAssocID,
				"newRouteTableId", newRtbID, "accountID", accountID)

			return &ec2.ReplaceRouteTableAssociationOutput{
				NewAssociationId: aws.String(newAssocID),
				AssociationState: &ec2.RouteTableAssociationState{
					State: aws.String("associated"),
				},
			}, nil
		}
	}

	return nil, errors.New(awserrors.ErrorInvalidAssociationIDNotFound)
}

// matchesFilters checks if a route table record matches the given DescribeRouteTables filters
func matchesFilters(record *RouteTableRecord, filters map[string][]string) bool {
	for name, values := range filters {
		switch name {
		case "vpc-id":
			if !containsString(values, record.VpcId) {
				return false
			}
		case "route-table-id":
			if !containsString(values, record.RouteTableId) {
				return false
			}
		case "association.main":
			wantMain := containsString(values, "true")
			hasMain := false
			for _, a := range record.Associations {
				if a.Main {
					hasMain = true
					break
				}
			}
			if wantMain != hasMain {
				return false
			}
		case "association.subnet-id":
			found := false
			for _, a := range record.Associations {
				if containsString(values, a.SubnetId) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		case "route.destination-cidr-block":
			found := false
			for _, r := range record.Routes {
				if containsString(values, r.DestinationCidrBlock) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		case "route.gateway-id":
			found := false
			for _, r := range record.Routes {
				if containsString(values, r.GatewayId) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

func containsString(slice []string, s string) bool {
	return slices.Contains(slice, s)
}

// recordToEC2 converts an internal record to an AWS SDK RouteTable struct
func recordToEC2(record *RouteTableRecord) *ec2.RouteTable {
	rtb := &ec2.RouteTable{
		RouteTableId: aws.String(record.RouteTableId),
		VpcId:        aws.String(record.VpcId),
		OwnerId:      aws.String(record.AccountID),
	}

	for _, r := range record.Routes {
		route := &ec2.Route{
			DestinationCidrBlock: aws.String(r.DestinationCidrBlock),
			State:                aws.String(r.State),
			Origin:               aws.String(r.Origin),
		}
		if r.GatewayId != "" {
			route.GatewayId = aws.String(r.GatewayId)
		}
		if r.NatGatewayId != "" {
			route.NatGatewayId = aws.String(r.NatGatewayId)
		}
		rtb.Routes = append(rtb.Routes, route)
	}

	for _, a := range record.Associations {
		assoc := &ec2.RouteTableAssociation{
			RouteTableAssociationId: aws.String(a.AssociationId),
			RouteTableId:            aws.String(record.RouteTableId),
			Main:                    aws.Bool(a.Main),
			AssociationState: &ec2.RouteTableAssociationState{
				State: aws.String("associated"),
			},
		}
		if a.SubnetId != "" {
			assoc.SubnetId = aws.String(a.SubnetId)
		}
		rtb.Associations = append(rtb.Associations, assoc)
	}

	if len(record.Tags) > 0 {
		tags := make([]*ec2.Tag, 0, len(record.Tags))
		for k, v := range record.Tags {
			tags = append(tags, &ec2.Tag{Key: aws.String(k), Value: aws.String(v)})
		}
		rtb.Tags = tags
	}

	return rtb
}
