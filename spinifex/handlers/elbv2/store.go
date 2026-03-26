package handlers_elbv2

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/nats-io/nats.go"
)

const (
	KVBucketELBv2        = "spinifex-elbv2"
	KVBucketELBv2Version = 1

	// Key prefixes for different resource types within the single bucket
	KeyPrefixLB       = "lb."
	KeyPrefixTG       = "tg."
	KeyPrefixListener = "listener."
)

// Store provides CRUD operations for ELBv2 resources backed by JetStream KV.
type Store struct {
	kv nats.KeyValue
}

// NewStore creates a new ELBv2 store using the provided NATS connection.
func NewStore(nc *nats.Conn) (*Store, error) {
	js, err := nc.JetStream()
	if err != nil {
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	kv, err := utils.GetOrCreateKVBucket(js, KVBucketELBv2, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to create KV bucket %s: %w", KVBucketELBv2, err)
	}

	if err := utils.WriteVersion(kv, KVBucketELBv2Version); err != nil {
		return nil, fmt.Errorf("write version to %s: %w", KVBucketELBv2, err)
	}

	slog.Info("ELBv2 store initialized", "bucket", KVBucketELBv2)
	return &Store{kv: kv}, nil
}

// --- Load Balancer CRUD ---

// PutLoadBalancer stores a load balancer record.
func (s *Store) PutLoadBalancer(lb *LoadBalancerRecord) error {
	data, err := json.Marshal(lb)
	if err != nil {
		return fmt.Errorf("marshal load balancer: %w", err)
	}
	_, err = s.kv.Put(KeyPrefixLB+lb.LoadBalancerID, data)
	return err
}

// GetLoadBalancer retrieves a load balancer by its short ID.
func (s *Store) GetLoadBalancer(lbID string) (*LoadBalancerRecord, error) {
	entry, err := s.kv.Get(KeyPrefixLB + lbID)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var lb LoadBalancerRecord
	if err := json.Unmarshal(entry.Value(), &lb); err != nil {
		return nil, fmt.Errorf("unmarshal load balancer: %w", err)
	}
	return &lb, nil
}

// DeleteLoadBalancer removes a load balancer by its short ID.
func (s *Store) DeleteLoadBalancer(lbID string) error {
	err := s.kv.Delete(KeyPrefixLB + lbID)
	if err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
		return err
	}
	return nil
}

// ListLoadBalancers returns all load balancer records.
func (s *Store) ListLoadBalancers() ([]*LoadBalancerRecord, error) {
	return listByPrefix[LoadBalancerRecord](s.kv, KeyPrefixLB)
}

// GetLoadBalancerByArn finds a load balancer by its ARN.
func (s *Store) GetLoadBalancerByArn(arn string) (*LoadBalancerRecord, error) {
	lbs, err := s.ListLoadBalancers()
	if err != nil {
		return nil, err
	}
	for _, lb := range lbs {
		if lb.LoadBalancerArn == arn {
			return lb, nil
		}
	}
	return nil, nil
}

// GetLoadBalancerByName finds a load balancer by name.
func (s *Store) GetLoadBalancerByName(name string) (*LoadBalancerRecord, error) {
	lbs, err := s.ListLoadBalancers()
	if err != nil {
		return nil, err
	}
	for _, lb := range lbs {
		if lb.Name == name {
			return lb, nil
		}
	}
	return nil, nil
}

// --- Target Group CRUD ---

// PutTargetGroup stores a target group record.
func (s *Store) PutTargetGroup(tg *TargetGroupRecord) error {
	data, err := json.Marshal(tg)
	if err != nil {
		return fmt.Errorf("marshal target group: %w", err)
	}
	_, err = s.kv.Put(KeyPrefixTG+tg.TargetGroupID, data)
	return err
}

// GetTargetGroup retrieves a target group by its short ID.
func (s *Store) GetTargetGroup(tgID string) (*TargetGroupRecord, error) {
	entry, err := s.kv.Get(KeyPrefixTG + tgID)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var tg TargetGroupRecord
	if err := json.Unmarshal(entry.Value(), &tg); err != nil {
		return nil, fmt.Errorf("unmarshal target group: %w", err)
	}
	return &tg, nil
}

// DeleteTargetGroup removes a target group by its short ID.
func (s *Store) DeleteTargetGroup(tgID string) error {
	err := s.kv.Delete(KeyPrefixTG + tgID)
	if err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
		return err
	}
	return nil
}

// ListTargetGroups returns all target group records.
func (s *Store) ListTargetGroups() ([]*TargetGroupRecord, error) {
	return listByPrefix[TargetGroupRecord](s.kv, KeyPrefixTG)
}

// GetTargetGroupByArn finds a target group by its ARN.
func (s *Store) GetTargetGroupByArn(arn string) (*TargetGroupRecord, error) {
	tgs, err := s.ListTargetGroups()
	if err != nil {
		return nil, err
	}
	for _, tg := range tgs {
		if tg.TargetGroupArn == arn {
			return tg, nil
		}
	}
	return nil, nil
}

// GetTargetGroupByName finds a target group by name within a VPC.
func (s *Store) GetTargetGroupByName(name, vpcID string) (*TargetGroupRecord, error) {
	tgs, err := s.ListTargetGroups()
	if err != nil {
		return nil, err
	}
	for _, tg := range tgs {
		if tg.Name == name && tg.VpcId == vpcID {
			return tg, nil
		}
	}
	return nil, nil
}

// --- Listener CRUD ---

// PutListener stores a listener record.
func (s *Store) PutListener(l *ListenerRecord) error {
	data, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("marshal listener: %w", err)
	}
	_, err = s.kv.Put(KeyPrefixListener+l.ListenerID, data)
	return err
}

// GetListener retrieves a listener by its short ID.
func (s *Store) GetListener(listenerID string) (*ListenerRecord, error) {
	entry, err := s.kv.Get(KeyPrefixListener + listenerID)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var l ListenerRecord
	if err := json.Unmarshal(entry.Value(), &l); err != nil {
		return nil, fmt.Errorf("unmarshal listener: %w", err)
	}
	return &l, nil
}

// DeleteListener removes a listener by its short ID.
func (s *Store) DeleteListener(listenerID string) error {
	err := s.kv.Delete(KeyPrefixListener + listenerID)
	if err != nil && !errors.Is(err, nats.ErrKeyNotFound) {
		return err
	}
	return nil
}

// ListListeners returns all listener records.
func (s *Store) ListListeners() ([]*ListenerRecord, error) {
	return listByPrefix[ListenerRecord](s.kv, KeyPrefixListener)
}

// ListListenersByLB returns all listeners for a specific load balancer ARN.
func (s *Store) ListListenersByLB(lbArn string) ([]*ListenerRecord, error) {
	all, err := s.ListListeners()
	if err != nil {
		return nil, err
	}
	var result []*ListenerRecord
	for _, l := range all {
		if l.LoadBalancerArn == lbArn {
			result = append(result, l)
		}
	}
	return result, nil
}

// GetListenerByArn finds a listener by its ARN.
func (s *Store) GetListenerByArn(arn string) (*ListenerRecord, error) {
	listeners, err := s.ListListeners()
	if err != nil {
		return nil, err
	}
	for _, l := range listeners {
		if l.ListenerArn == arn {
			return l, nil
		}
	}
	return nil, nil
}

// --- Generic helpers ---

// listByPrefix returns all records with keys matching the given prefix.
func listByPrefix[T any](kv nats.KeyValue, prefix string) ([]*T, error) {
	keys, err := kv.Keys()
	if err != nil {
		if errors.Is(err, nats.ErrNoKeysFound) {
			return nil, nil
		}
		return nil, err
	}

	var result []*T
	for _, key := range keys {
		if key == utils.VersionKey {
			continue
		}
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		entry, err := kv.Get(key)
		if err != nil {
			if errors.Is(err, nats.ErrKeyNotFound) {
				continue
			}
			return nil, err
		}

		var record T
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			slog.Error("Failed to unmarshal ELBv2 record", "key", key, "err", err)
			continue
		}

		result = append(result, &record)
	}

	return result, nil
}
