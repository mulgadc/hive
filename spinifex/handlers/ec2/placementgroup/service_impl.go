package handlers_ec2_placementgroup

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/config"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

// Ensure PlacementGroupServiceImpl implements PlacementGroupService
var _ PlacementGroupService = (*PlacementGroupServiceImpl)(nil)

const (
	KVBucketPlacementGroups        = "spinifex-placement-groups"
	KVBucketPlacementGroupsVersion = 1
)

// PlacementGroupRecord represents a stored placement group.
type PlacementGroupRecord struct {
	GroupId   string `json:"group_id"`
	GroupName string `json:"group_name"`
	Strategy  string `json:"strategy"`
	State     string `json:"state"`
	// SpreadLevel is always "host" for bare-metal Spinifex clusters.
	SpreadLevel string `json:"spread_level"`
	AccountID   string `json:"account_id"`
	// NodeInstances tracks which node hosts which instances in this group.
	// Key = node name, Value = list of instance IDs on that node.
	NodeInstances map[string][]string `json:"node_instances"`
}

// PlacementGroupServiceImpl implements placement group operations with NATS JetStream persistence.
type PlacementGroupServiceImpl struct {
	config   *config.Config
	natsConn *nats.Conn
	kv       nats.KeyValue
}

// NewPlacementGroupServiceImplWithNATS creates a placement group service with NATS JetStream.
func NewPlacementGroupServiceImplWithNATS(cfg *config.Config, natsConn *nats.Conn) (*PlacementGroupServiceImpl, error) {
	js, err := natsConn.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	kv, err := utils.GetOrCreateKVBucket(js, KVBucketPlacementGroups, 10)
	if err != nil {
		return nil, fmt.Errorf("failed to create KV bucket %s: %w", KVBucketPlacementGroups, err)
	}
	if err := utils.WriteVersion(kv, KVBucketPlacementGroupsVersion); err != nil {
		return nil, fmt.Errorf("write version to %s: %w", KVBucketPlacementGroups, err)
	}

	slog.Info("Placement group service initialized with JetStream KV", "bucket", KVBucketPlacementGroups)

	return &PlacementGroupServiceImpl{
		config:   cfg,
		natsConn: natsConn,
		kv:       kv,
	}, nil
}

// CreatePlacementGroup creates a new placement group.
func (s *PlacementGroupServiceImpl) CreatePlacementGroup(input *ec2.CreatePlacementGroupInput, accountID string) (*ec2.CreatePlacementGroupOutput, error) {
	if input.GroupName == nil || *input.GroupName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	strategy := aws.StringValue(input.Strategy)
	if strategy == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	// Only spread and cluster are supported; partition is rejected.
	if strategy == "partition" {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}
	if strategy != "spread" && strategy != "cluster" {
		return nil, errors.New(awserrors.ErrorInvalidParameterValue)
	}

	groupName := *input.GroupName

	// Check for duplicate name within account
	key := utils.AccountKey(accountID, groupName)
	if _, err := s.kv.Get(key); err == nil {
		return nil, errors.New(awserrors.ErrorInvalidPlacementGroupDuplicate)
	}

	groupID := utils.GenerateResourceID("pg")

	record := PlacementGroupRecord{
		GroupId:       groupID,
		GroupName:     groupName,
		Strategy:      strategy,
		State:         "available",
		SpreadLevel:   "host",
		AccountID:     accountID,
		NodeInstances: make(map[string][]string),
	}

	data, err := json.Marshal(record)
	if err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}
	if _, err := s.kv.Put(key, data); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("CreatePlacementGroup completed", "groupId", groupID, "groupName", groupName, "strategy", strategy, "accountID", accountID)

	return &ec2.CreatePlacementGroupOutput{
		PlacementGroup: s.recordToEC2(&record),
	}, nil
}

// DeletePlacementGroup deletes a placement group.
func (s *PlacementGroupServiceImpl) DeletePlacementGroup(input *ec2.DeletePlacementGroupInput, accountID string) (*ec2.DeletePlacementGroupOutput, error) {
	if input.GroupName == nil || *input.GroupName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	groupName := *input.GroupName
	key := utils.AccountKey(accountID, groupName)

	entry, err := s.kv.Get(key)
	if err != nil {
		return nil, errors.New(awserrors.ErrorInvalidPlacementGroupUnknown)
	}

	var record PlacementGroupRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	// Check for running instances
	instanceCount := 0
	for _, ids := range record.NodeInstances {
		instanceCount += len(ids)
	}
	if instanceCount > 0 {
		return nil, errors.New(awserrors.ErrorInvalidPlacementGroupInUse)
	}

	if err := s.kv.Delete(key); err != nil {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("DeletePlacementGroup completed", "groupName", groupName, "accountID", accountID)

	return &ec2.DeletePlacementGroupOutput{}, nil
}

// DescribePlacementGroups lists placement groups with optional filters.
func (s *PlacementGroupServiceImpl) DescribePlacementGroups(input *ec2.DescribePlacementGroupsInput, accountID string) (*ec2.DescribePlacementGroupsOutput, error) {
	// Build filter maps
	nameSet := make(map[string]bool)
	for _, name := range input.GroupNames {
		if name != nil {
			nameSet[*name] = true
		}
	}
	idSet := make(map[string]bool)
	for _, id := range input.GroupIds {
		if id != nil {
			idSet[*id] = true
		}
	}

	// Extract filter values
	filterStrategy := ""
	filterState := ""
	filterSpreadLevel := ""
	filterGroupName := ""
	for _, f := range input.Filters {
		if f.Name == nil || len(f.Values) == 0 || f.Values[0] == nil {
			continue
		}
		switch *f.Name {
		case "strategy":
			filterStrategy = *f.Values[0]
		case "state":
			filterState = *f.Values[0]
		case "spread-level":
			filterSpreadLevel = *f.Values[0]
		case "group-name":
			filterGroupName = *f.Values[0]
		}
	}

	prefix := accountID + "."
	keys, err := s.kv.Keys()
	if err != nil && !errors.Is(err, nats.ErrNoKeysFound) {
		return nil, errors.New(awserrors.ErrorServerInternal)
	}

	var groups []*ec2.PlacementGroup
	for _, k := range keys {
		if k == utils.VersionKey {
			continue
		}
		if !strings.HasPrefix(k, prefix) {
			continue
		}

		entry, err := s.kv.Get(k)
		if err != nil {
			slog.Warn("Failed to get placement group record", "key", k, "error", err)
			continue
		}

		var record PlacementGroupRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			slog.Warn("Failed to unmarshal placement group record", "key", k, "error", err)
			continue
		}

		// Apply name filter (from GroupNames parameter)
		if len(nameSet) > 0 && !nameSet[record.GroupName] {
			continue
		}
		// Apply ID filter (from GroupIds parameter)
		if len(idSet) > 0 && !idSet[record.GroupId] {
			continue
		}
		// Apply Filters
		if filterStrategy != "" && record.Strategy != filterStrategy {
			continue
		}
		if filterState != "" && record.State != filterState {
			continue
		}
		if filterSpreadLevel != "" && record.SpreadLevel != filterSpreadLevel {
			continue
		}
		if filterGroupName != "" && record.GroupName != filterGroupName {
			continue
		}

		groups = append(groups, s.recordToEC2(&record))
	}

	// If specific names were requested but not found, return error
	if len(nameSet) > 0 {
		found := make(map[string]bool)
		for _, g := range groups {
			if g.GroupName != nil {
				found[*g.GroupName] = true
			}
		}
		for name := range nameSet {
			if !found[name] {
				return nil, errors.New(awserrors.ErrorInvalidPlacementGroupUnknown)
			}
		}
	}

	slog.Info("DescribePlacementGroups completed", "count", len(groups), "accountID", accountID)

	return &ec2.DescribePlacementGroupsOutput{
		PlacementGroups: groups,
	}, nil
}

// GetPlacementGroupRecord reads a placement group record from KV with its revision for CAS operations.
// Returns the record and the KV entry (for revision). Exported for use by gateway spread routing.
func (s *PlacementGroupServiceImpl) GetPlacementGroupRecord(accountID, groupName string) (*PlacementGroupRecord, nats.KeyValueEntry, error) {
	key := utils.AccountKey(accountID, groupName)
	entry, err := s.kv.Get(key)
	if err != nil {
		return nil, nil, errors.New(awserrors.ErrorInvalidPlacementGroupUnknown)
	}

	var record PlacementGroupRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, nil, errors.New(awserrors.ErrorServerInternal)
	}

	return &record, entry, nil
}

// UpdatePlacementGroupRecord writes a placement group record using CAS (optimistic concurrency).
// Returns nil on success or the error on CAS conflict.
func (s *PlacementGroupServiceImpl) UpdatePlacementGroupRecord(accountID, groupName string, record *PlacementGroupRecord, revision uint64) error {
	key := utils.AccountKey(accountID, groupName)
	data, err := json.Marshal(record)
	if err != nil {
		return errors.New(awserrors.ErrorServerInternal)
	}
	if _, err := s.kv.Update(key, data, revision); err != nil {
		return err
	}
	return nil
}

const maxCASRetries = 5

// ReserveSpreadNodes atomically reserves node slots for a spread placement group launch.
// It reads the group record, filters eligible nodes (excluding already-occupied ones),
// selects nodes, writes placeholder entries, and returns the selected nodes.
// Uses CAS with retries following the IPAM pattern.
func (s *PlacementGroupServiceImpl) ReserveSpreadNodes(input *ReserveSpreadNodesInput, accountID string) (*ReserveSpreadNodesOutput, error) {
	if input.GroupName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	for attempt := range maxCASRetries {
		record, entry, err := s.GetPlacementGroupRecord(accountID, input.GroupName)
		if err != nil {
			return nil, err
		}

		if record.State != "available" {
			return nil, errors.New(awserrors.ErrorInvalidPlacementGroupUnknown)
		}
		if record.Strategy != "spread" {
			return nil, errors.New(awserrors.ErrorInvalidParameterValue)
		}

		// Build set of nodes already hosting instances in this group
		occupiedNodes := make(map[string]bool)
		for node := range record.NodeInstances {
			occupiedNodes[node] = true
		}

		// Filter eligible nodes: must have capacity AND not already occupied
		var available []string
		for _, node := range input.EligibleNodes {
			if !occupiedNodes[node] {
				available = append(available, node)
			}
		}

		if len(available) < input.MinCount {
			return nil, errors.New(awserrors.ErrorInsufficientInstanceCapacity)
		}

		// Select nodes: up to MaxCount, at least MinCount
		launchCount := min(input.MaxCount, len(available))
		selected := available[:launchCount]

		// Add placeholder entries (empty instance list = reserved but not yet launched)
		for _, node := range selected {
			record.NodeInstances[node] = []string{}
		}

		// CAS write — retry on conflict
		if err := s.UpdatePlacementGroupRecord(accountID, input.GroupName, record, entry.Revision()); err != nil {
			slog.Debug("ReserveSpreadNodes: CAS conflict, retrying", "attempt", attempt, "err", err)
			continue
		}

		slog.Info("ReserveSpreadNodes completed", "groupName", input.GroupName, "nodes", selected, "accountID", accountID)
		return &ReserveSpreadNodesOutput{ReservedNodes: selected}, nil
	}

	return nil, errors.New(awserrors.ErrorServerInternal)
}

// FinalizeSpreadInstances replaces placeholder entries with actual instance IDs.
// Uses CAS with retries following the IPAM pattern.
func (s *PlacementGroupServiceImpl) FinalizeSpreadInstances(input *FinalizeSpreadInstancesInput, accountID string) (*FinalizeSpreadInstancesOutput, error) {
	if input.GroupName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	for attempt := range maxCASRetries {
		record, entry, err := s.GetPlacementGroupRecord(accountID, input.GroupName)
		if err != nil {
			return nil, err
		}

		maps.Copy(record.NodeInstances, input.NodeInstances)

		if err := s.UpdatePlacementGroupRecord(accountID, input.GroupName, record, entry.Revision()); err != nil {
			slog.Debug("FinalizeSpreadInstances: CAS conflict, retrying", "attempt", attempt, "err", err)
			continue
		}

		slog.Info("FinalizeSpreadInstances completed", "groupName", input.GroupName, "accountID", accountID)
		return &FinalizeSpreadInstancesOutput{}, nil
	}

	return nil, errors.New(awserrors.ErrorServerInternal)
}

// ReleaseSpreadNodes removes placeholder entries for nodes that failed to launch.
// Uses CAS with retries following the IPAM pattern.
func (s *PlacementGroupServiceImpl) ReleaseSpreadNodes(input *ReleaseSpreadNodesInput, accountID string) (*ReleaseSpreadNodesOutput, error) {
	if input.GroupName == "" {
		return nil, errors.New(awserrors.ErrorMissingParameter)
	}

	releaseSet := make(map[string]bool, len(input.Nodes))
	for _, n := range input.Nodes {
		releaseSet[n] = true
	}

	for attempt := range maxCASRetries {
		record, entry, err := s.GetPlacementGroupRecord(accountID, input.GroupName)
		if err != nil {
			return nil, err
		}

		for node := range releaseSet {
			delete(record.NodeInstances, node)
		}

		if err := s.UpdatePlacementGroupRecord(accountID, input.GroupName, record, entry.Revision()); err != nil {
			slog.Debug("ReleaseSpreadNodes: CAS conflict, retrying", "attempt", attempt, "err", err)
			continue
		}

		slog.Info("ReleaseSpreadNodes completed", "groupName", input.GroupName, "nodes", input.Nodes, "accountID", accountID)
		return &ReleaseSpreadNodesOutput{}, nil
	}

	return nil, errors.New(awserrors.ErrorServerInternal)
}

// recordToEC2 converts an internal record to the AWS SDK PlacementGroup type.
func (s *PlacementGroupServiceImpl) recordToEC2(record *PlacementGroupRecord) *ec2.PlacementGroup {
	pg := &ec2.PlacementGroup{
		GroupId:   aws.String(record.GroupId),
		GroupName: aws.String(record.GroupName),
		Strategy:  aws.String(record.Strategy),
		State:     aws.String(record.State),
	}
	if record.Strategy == "spread" {
		pg.SpreadLevel = aws.String(record.SpreadLevel)
	}
	return pg
}
