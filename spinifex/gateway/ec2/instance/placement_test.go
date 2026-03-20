package gateway_ec2_instance

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/types"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- spreadAllocate tests (pure algorithm) ---

func TestSpreadAllocate_EqualDistribution(t *testing.T) {
	// 3 instances across 3 nodes → 1 per node
	nodes := []nodeAllocation{
		{NodeID: "A", Available: 4},
		{NodeID: "B", Available: 3},
		{NodeID: "C", Available: 2},
	}
	result := spreadAllocate(nodes, 3)

	assert.Len(t, result, 3)
	for _, a := range result {
		assert.Equal(t, 1, a.Assigned, "node %s should get exactly 1", a.NodeID)
	}
}

func TestSpreadAllocate_SpreadThenPack(t *testing.T) {
	// 5 instances across 3 nodes (capacities: A=4, B=3, C=2)
	// Round 1: A=1, B=1, C=1 (3 assigned, 2 remaining)
	// Round 2: A gets 1 (remaining cap 3), B gets 1 (remaining cap 2)
	nodes := []nodeAllocation{
		{NodeID: "A", Available: 4},
		{NodeID: "B", Available: 3},
		{NodeID: "C", Available: 2},
	}
	result := spreadAllocate(nodes, 5)

	assert.Len(t, result, 3)
	byNode := make(map[string]int)
	for _, a := range result {
		byNode[a.NodeID] = a.Assigned
	}
	assert.Equal(t, 2, byNode["A"], "node A (cap 4) should get 2")
	assert.Equal(t, 2, byNode["B"], "node B (cap 3) should get 2")
	assert.Equal(t, 1, byNode["C"], "node C (cap 2) should get 1")
}

func TestSpreadAllocate_SingleNode(t *testing.T) {
	// All 3 instances on 1 node
	nodes := []nodeAllocation{
		{NodeID: "A", Available: 5},
	}
	result := spreadAllocate(nodes, 3)

	assert.Len(t, result, 1)
	assert.Equal(t, "A", result[0].NodeID)
	assert.Equal(t, 3, result[0].Assigned)
}

func TestSpreadAllocate_MoreNodesThanInstances(t *testing.T) {
	// 2 instances across 5 nodes → only 2 get assigned
	nodes := []nodeAllocation{
		{NodeID: "A", Available: 4},
		{NodeID: "B", Available: 3},
		{NodeID: "C", Available: 2},
		{NodeID: "D", Available: 2},
		{NodeID: "E", Available: 1},
	}
	result := spreadAllocate(nodes, 2)

	assert.Len(t, result, 2)
	totalAssigned := 0
	for _, a := range result {
		assert.Equal(t, 1, a.Assigned)
		totalAssigned += a.Assigned
	}
	assert.Equal(t, 2, totalAssigned)
}

func TestSpreadAllocate_HeavyPacking(t *testing.T) {
	// 10 instances across 2 nodes (A=8, B=6)
	// Round 1: A=1, B=1
	// Packing: each round picks node with most remaining
	nodes := []nodeAllocation{
		{NodeID: "A", Available: 8},
		{NodeID: "B", Available: 6},
	}
	result := spreadAllocate(nodes, 10)

	assert.Len(t, result, 2)
	byNode := make(map[string]int)
	for _, a := range result {
		byNode[a.NodeID] = a.Assigned
	}
	total := byNode["A"] + byNode["B"]
	assert.Equal(t, 10, total)
	// A has more capacity so should get more
	assert.GreaterOrEqual(t, byNode["A"], byNode["B"])
}

func TestSpreadAllocate_ExactCapacity(t *testing.T) {
	// Request exactly matches total capacity
	nodes := []nodeAllocation{
		{NodeID: "A", Available: 2},
		{NodeID: "B", Available: 1},
	}
	result := spreadAllocate(nodes, 3)

	assert.Len(t, result, 2)
	byNode := make(map[string]int)
	for _, a := range result {
		byNode[a.NodeID] = a.Assigned
	}
	assert.Equal(t, 2, byNode["A"])
	assert.Equal(t, 1, byNode["B"])
}

func TestSpreadAllocate_ZeroCount(t *testing.T) {
	nodes := []nodeAllocation{
		{NodeID: "A", Available: 4},
	}
	result := spreadAllocate(nodes, 0)
	assert.Len(t, result, 0)
}

func TestSpreadAllocate_EmptyNodes(t *testing.T) {
	result := spreadAllocate(nil, 3)
	assert.Len(t, result, 0)
}

// --- queryNodeCapacity tests (NATS-based) ---

func TestQueryNodeCapacity_FiltersEligibleNodes(t *testing.T) {
	_, nc := startTestNATSServer(t)

	// Simulate 3 daemons responding to spinifex.node.status
	sub, err := nc.Subscribe("spinifex.node.status", func(msg *nats.Msg) {
		// Respond 3 times with different node statuses
		responses := []types.NodeStatusResponse{
			{
				Node: "node-1",
				InstanceTypes: []types.InstanceTypeCap{
					{Name: "t3.micro", Available: 4},
					{Name: "t3.small", Available: 2},
				},
			},
			{
				Node: "node-2",
				InstanceTypes: []types.InstanceTypeCap{
					{Name: "t3.micro", Available: 0}, // no capacity
					{Name: "t3.small", Available: 3},
				},
			},
			{
				Node: "node-3",
				InstanceTypes: []types.InstanceTypeCap{
					{Name: "t3.micro", Available: 2},
				},
			},
		}
		for _, resp := range responses {
			data, _ := json.Marshal(resp)
			_ = nc.Publish(msg.Reply, data)
		}
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Query for t3.micro — should get node-1 (cap 4) and node-3 (cap 2), not node-2 (cap 0)
	nodes, err := queryNodeCapacity(nc, "t3.micro")
	require.NoError(t, err)

	assert.Len(t, nodes, 2)
	// Should be sorted by capacity descending
	assert.Equal(t, "node-1", nodes[0].NodeID)
	assert.Equal(t, 4, nodes[0].Available)
	assert.Equal(t, "node-3", nodes[1].NodeID)
	assert.Equal(t, 2, nodes[1].Available)
}

func TestQueryNodeCapacity_NoNodes(t *testing.T) {
	_, nc := startTestNATSServer(t)

	// No subscribers → timeout, empty result
	nodes, err := queryNodeCapacity(nc, "t3.micro")
	require.NoError(t, err)
	assert.Len(t, nodes, 0)
}

// --- aggregateResults tests ---

func TestAggregateResults_AllSucceed(t *testing.T) {
	results := []nodeLaunchResult{
		{
			NodeID: "node-1",
			Reservation: &ec2.Reservation{
				ReservationId: aws.String("r-abc"),
				Instances: []*ec2.Instance{
					{InstanceId: aws.String("i-001")},
				},
			},
		},
		{
			NodeID: "node-2",
			Reservation: &ec2.Reservation{
				ReservationId: aws.String("r-def"),
				Instances: []*ec2.Instance{
					{InstanceId: aws.String("i-002")},
					{InstanceId: aws.String("i-003")},
				},
			},
		},
	}

	// No NATS needed — all succeed, no rollback
	reservation, err := aggregateResults(results, 2, nil, "")
	require.NoError(t, err)
	assert.Len(t, reservation.Instances, 3)
	assert.Equal(t, "r-abc", aws.StringValue(reservation.ReservationId))
}

func TestAggregateResults_PartialSuccessMeetsMinCount(t *testing.T) {
	results := []nodeLaunchResult{
		{
			NodeID: "node-1",
			Reservation: &ec2.Reservation{
				ReservationId: aws.String("r-abc"),
				Instances: []*ec2.Instance{
					{InstanceId: aws.String("i-001")},
					{InstanceId: aws.String("i-002")},
				},
			},
		},
		{
			NodeID: "node-2",
			Err:    assert.AnError,
		},
	}

	// MinCount=2, got 2 from node-1 → success
	reservation, err := aggregateResults(results, 2, nil, "")
	require.NoError(t, err)
	assert.Len(t, reservation.Instances, 2)
}

func TestAggregateResults_PartialFailureBelowMinCount(t *testing.T) {
	_, nc := startTestNATSServer(t)

	results := []nodeLaunchResult{
		{
			NodeID: "node-1",
			Reservation: &ec2.Reservation{
				Instances: []*ec2.Instance{
					{InstanceId: aws.String("i-001")},
				},
			},
		},
		{
			NodeID: "node-2",
			Err:    assert.AnError,
		},
	}

	// MinCount=3, only got 1 → should fail with InsufficientInstanceCapacity
	// Note: rollback will attempt to terminate i-001 but we don't have a
	// daemon responding, so it will fail silently — that's OK for this test
	_, err := aggregateResults(results, 3, nc, "test-account")
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInsufficientInstanceCapacity, err.Error())
}

func TestAggregateResults_AllFail(t *testing.T) {
	results := []nodeLaunchResult{
		{NodeID: "node-1", Err: assert.AnError},
		{NodeID: "node-2", Err: assert.AnError},
	}

	_, err := aggregateResults(results, 1, nil, "")
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInsufficientInstanceCapacity, err.Error())
}

// --- distributeInstances integration tests (end-to-end with mock daemons) ---

func TestDistributeInstances_SuccessfulSpread(t *testing.T) {
	_, nc := startTestNATSServer(t)

	// Mock node.status responder
	statusSub, err := nc.Subscribe("spinifex.node.status", func(msg *nats.Msg) {
		for _, resp := range []types.NodeStatusResponse{
			{Node: "node-1", InstanceTypes: []types.InstanceTypeCap{{Name: "t3.micro", Available: 2}}},
			{Node: "node-2", InstanceTypes: []types.InstanceTypeCap{{Name: "t3.micro", Available: 2}}},
		} {
			data, _ := json.Marshal(resp)
			_ = nc.Publish(msg.Reply, data)
		}
	})
	require.NoError(t, err)
	defer statusSub.Unsubscribe()

	// Mock daemon on node-1
	sub1, err := nc.Subscribe("ec2.RunInstances.t3.micro.node-1", func(msg *nats.Msg) {
		reservation := ec2.Reservation{
			ReservationId: aws.String("r-test1"),
			Instances:     []*ec2.Instance{{InstanceId: aws.String("i-n1")}},
		}
		data, _ := json.Marshal(reservation)
		_ = msg.Respond(data)
	})
	require.NoError(t, err)
	defer sub1.Unsubscribe()

	// Mock daemon on node-2
	sub2, err := nc.Subscribe("ec2.RunInstances.t3.micro.node-2", func(msg *nats.Msg) {
		reservation := ec2.Reservation{
			ReservationId: aws.String("r-test2"),
			Instances:     []*ec2.Instance{{InstanceId: aws.String("i-n2")}},
		}
		data, _ := json.Marshal(reservation)
		_ = msg.Respond(data)
	})
	require.NoError(t, err)
	defer sub2.Unsubscribe()

	time.Sleep(50 * time.Millisecond) // let subscriptions propagate

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-test"),
		InstanceType: aws.String("t3.micro"),
		MinCount:     aws.Int64(2),
		MaxCount:     aws.Int64(2),
	}

	reservation, err := distributeInstances(input, nc, "test-account")
	require.NoError(t, err)
	assert.Len(t, reservation.Instances, 2)

	// Verify instances came from different nodes
	ids := make(map[string]bool)
	for _, inst := range reservation.Instances {
		ids[aws.StringValue(inst.InstanceId)] = true
	}
	assert.True(t, ids["i-n1"], "should have instance from node-1")
	assert.True(t, ids["i-n2"], "should have instance from node-2")
}

func TestDistributeInstances_InsufficientCapacity(t *testing.T) {
	_, nc := startTestNATSServer(t)

	// Mock node.status with only 1 available slot total
	statusSub, err := nc.Subscribe("spinifex.node.status", func(msg *nats.Msg) {
		resp := types.NodeStatusResponse{
			Node:          "node-1",
			InstanceTypes: []types.InstanceTypeCap{{Name: "t3.micro", Available: 1}},
		}
		data, _ := json.Marshal(resp)
		_ = nc.Publish(msg.Reply, data)
	})
	require.NoError(t, err)
	defer statusSub.Unsubscribe()

	time.Sleep(50 * time.Millisecond)

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-test"),
		InstanceType: aws.String("t3.micro"),
		MinCount:     aws.Int64(3),
		MaxCount:     aws.Int64(3),
	}

	_, err = distributeInstances(input, nc, "test-account")
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInsufficientInstanceCapacity, err.Error())
}

func TestDistributeInstances_LaunchCountCappedToMaxCount(t *testing.T) {
	_, nc := startTestNATSServer(t)

	// 3 nodes with capacity, but MaxCount=2
	statusSub, err := nc.Subscribe("spinifex.node.status", func(msg *nats.Msg) {
		for _, resp := range []types.NodeStatusResponse{
			{Node: "node-1", InstanceTypes: []types.InstanceTypeCap{{Name: "t3.micro", Available: 4}}},
			{Node: "node-2", InstanceTypes: []types.InstanceTypeCap{{Name: "t3.micro", Available: 3}}},
			{Node: "node-3", InstanceTypes: []types.InstanceTypeCap{{Name: "t3.micro", Available: 2}}},
		} {
			data, _ := json.Marshal(resp)
			_ = nc.Publish(msg.Reply, data)
		}
	})
	require.NoError(t, err)
	defer statusSub.Unsubscribe()

	// Mock daemons — each returns 1 instance
	for _, nodeID := range []string{"node-1", "node-2"} {
		sub, err := nc.Subscribe("ec2.RunInstances.t3.micro."+nodeID, func(msg *nats.Msg) {
			reservation := ec2.Reservation{
				ReservationId: aws.String("r-" + nodeID),
				Instances:     []*ec2.Instance{{InstanceId: aws.String("i-" + nodeID)}},
			}
			data, _ := json.Marshal(reservation)
			_ = msg.Respond(data)
		})
		require.NoError(t, err)
		defer sub.Unsubscribe()
	}

	time.Sleep(50 * time.Millisecond)

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-test"),
		InstanceType: aws.String("t3.micro"),
		MinCount:     aws.Int64(1),
		MaxCount:     aws.Int64(2),
	}

	reservation, err := distributeInstances(input, nc, "test-account")
	require.NoError(t, err)
	// Should launch exactly 2 (MaxCount), not 3 (total capacity)
	assert.Len(t, reservation.Instances, 2)
}

func TestDistributeInstances_NoNodesAvailable(t *testing.T) {
	_, nc := startTestNATSServer(t)

	// No responders to node.status
	time.Sleep(50 * time.Millisecond)

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-test"),
		InstanceType: aws.String("t3.micro"),
		MinCount:     aws.Int64(2),
		MaxCount:     aws.Int64(2),
	}

	_, err := distributeInstances(input, nc, "test-account")
	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInsufficientInstanceCapacity, err.Error())
}

// --- RunInstances routing tests ---

func TestRunInstances_SingleInstanceUsesQueueGroup(t *testing.T) {
	// For MinCount=MaxCount=1, RunInstances should use the queue group path,
	// not the multi-node distribution path. We verify this by confirming it
	// does NOT query spinifex.node.status.
	_, nc := startTestNATSServer(t)

	statusQueried := false
	statusSub, err := nc.Subscribe("spinifex.node.status", func(msg *nats.Msg) {
		statusQueried = true
	})
	require.NoError(t, err)
	defer statusSub.Unsubscribe()

	// Mock the queue group handler
	queueSub, err := nc.QueueSubscribe("ec2.RunInstances.t3.micro", "spinifex-workers", func(msg *nats.Msg) {
		reservation := ec2.Reservation{
			ReservationId: aws.String("r-single"),
			Instances:     []*ec2.Instance{{InstanceId: aws.String("i-single")}},
		}
		data, _ := json.Marshal(reservation)
		_ = msg.Respond(data)
	})
	require.NoError(t, err)
	defer queueSub.Unsubscribe()

	time.Sleep(50 * time.Millisecond)

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-test"),
		InstanceType: aws.String("t3.micro"),
		MinCount:     aws.Int64(1),
		MaxCount:     aws.Int64(1),
	}

	reservation, err := RunInstances(input, nc, "test-account")
	require.NoError(t, err)
	assert.Len(t, reservation.Instances, 1)
	assert.Equal(t, "i-single", aws.StringValue(reservation.Instances[0].InstanceId))
	assert.False(t, statusQueried, "single-instance launch should NOT query node status")
}

func TestRunInstances_MultiInstanceUsesDistribution(t *testing.T) {
	// For MaxCount > 1, RunInstances should use the distribution path,
	// which queries spinifex.node.status.
	_, nc := startTestNATSServer(t)

	statusQueried := false
	statusSub, err := nc.Subscribe("spinifex.node.status", func(msg *nats.Msg) {
		statusQueried = true
		resp := types.NodeStatusResponse{
			Node:          "node-1",
			InstanceTypes: []types.InstanceTypeCap{{Name: "t3.micro", Available: 3}},
		}
		data, _ := json.Marshal(resp)
		_ = nc.Publish(msg.Reply, data)
	})
	require.NoError(t, err)
	defer statusSub.Unsubscribe()

	// Mock node-specific handler
	nodeSub, err := nc.Subscribe("ec2.RunInstances.t3.micro.node-1", func(msg *nats.Msg) {
		reservation := ec2.Reservation{
			ReservationId: aws.String("r-multi"),
			Instances: []*ec2.Instance{
				{InstanceId: aws.String("i-001")},
				{InstanceId: aws.String("i-002")},
			},
		}
		data, _ := json.Marshal(reservation)
		_ = msg.Respond(data)
	})
	require.NoError(t, err)
	defer nodeSub.Unsubscribe()

	time.Sleep(50 * time.Millisecond)

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-test"),
		InstanceType: aws.String("t3.micro"),
		MinCount:     aws.Int64(2),
		MaxCount:     aws.Int64(2),
	}

	reservation, err := RunInstances(input, nc, "test-account")
	require.NoError(t, err)
	assert.Len(t, reservation.Instances, 2)
	assert.True(t, statusQueried, "multi-instance launch should query node status")
}
