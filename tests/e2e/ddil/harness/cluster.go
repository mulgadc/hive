//go:build e2e

// Package harness exports the DDIL E2E test primitives: typed cluster/node
// descriptors, an SSH transport, fault-injection helpers, witness-VM helpers,
// snapshot/state assertions, and the daemon HTTP client used by scenarios.
//
// See docs/development/improvements/ddil-e2e-test-harness.md for the design.
package harness

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Node identifies a single cluster member addressable over SSH and HTTPS.
//
// Index is the 1-based position the node occupies in the DDIL_NODES list;
// scenarios reference nodes by index (node1, node2, node3) so a fixed
// ordering is required.
type Node struct {
	Index int    // 1-based position in the cluster
	Name  string // human-readable tag, e.g. "node1"
	Addr  string // IP or hostname; used for SSH and as peer address for partition rules
}

// Cluster is an ordered set of Nodes plus shared SSH credentials.
//
// Scenarios receive a *Cluster from main_test.go and use its Nodes slice to
// address individual peers. All helpers that mutate node state take a Node
// (not an index) to keep call sites self-describing.
type Cluster struct {
	Nodes      []Node
	SSHUser    string
	SSHKeyPath string
}

// Peers returns every node in the cluster except target. The result is used
// by PartitionNode to build per-peer iptables DROP rules.
func (c *Cluster) Peers(target Node) []Node {
	out := make([]Node, 0, len(c.Nodes)-1)
	for _, n := range c.Nodes {
		if n.Index == target.Index {
			continue
		}
		out = append(out, n)
	}
	return out
}

// ClusterFromEnv builds a Cluster from DDIL_NODES / DDIL_SSH_USER /
// DDIL_SSH_KEY. Nodes are named node1..nodeN in list order.
//
// Returns an error if any required variable is missing or if DDIL_NODES
// contains no non-empty entries. main_test.go is expected to call this once
// and pass the result into every scenario.
func ClusterFromEnv() (*Cluster, error) {
	nodesRaw := os.Getenv("DDIL_NODES")
	user := os.Getenv("DDIL_SSH_USER")
	key := os.Getenv("DDIL_SSH_KEY")

	var missing []string
	if nodesRaw == "" {
		missing = append(missing, "DDIL_NODES")
	}
	if user == "" {
		missing = append(missing, "DDIL_SSH_USER")
	}
	if key == "" {
		missing = append(missing, "DDIL_SSH_KEY")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("ddil harness: missing required env: %s", strings.Join(missing, ", "))
	}

	var nodes []Node
	for i, addr := range strings.Split(nodesRaw, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		nodes = append(nodes, Node{
			Index: i + 1,
			Name:  fmt.Sprintf("node%d", i+1),
			Addr:  addr,
		})
	}
	if len(nodes) == 0 {
		return nil, errors.New("ddil harness: DDIL_NODES contained no addresses")
	}

	return &Cluster{
		Nodes:      nodes,
		SSHUser:    user,
		SSHKeyPath: key,
	}, nil
}

// NodeFromEnv returns the Nth node (1-based) from the cluster defined by
// DDIL_NODES. Convenience wrapper for single-node helpers; most scenarios
// should call ClusterFromEnv once and index c.Nodes directly.
func NodeFromEnv(index int) (Node, error) {
	c, err := ClusterFromEnv()
	if err != nil {
		return Node{}, err
	}
	if index < 1 || index > len(c.Nodes) {
		return Node{}, fmt.Errorf("ddil harness: node index %d out of range (have %d nodes)", index, len(c.Nodes))
	}
	return c.Nodes[index-1], nil
}
