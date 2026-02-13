package formation

import (
	"sort"

	"github.com/mulgadc/hive/hive/admin"
)

// BuildClusterRoutes returns a sorted list of "IP:4248" NATS cluster routes
// for each node. If ClusterIP is set it is used, otherwise BindIP.
// Nodes are sorted by name for deterministic ordering.
func BuildClusterRoutes(nodes map[string]NodeInfo) []string {
	sorted := sortedNodes(nodes)
	routes := make([]string, len(sorted))
	for i, n := range sorted {
		ip := n.ClusterIP
		if ip == "" {
			ip = n.BindIP
		}
		routes[i] = ip + ":4248"
	}
	return routes
}

// BuildPredastoreNodes returns a sorted list of PredastoreNodeConfig with
// 1-based IDs. Nodes are sorted by name for deterministic ordering.
func BuildPredastoreNodes(nodes map[string]NodeInfo) []admin.PredastoreNodeConfig {
	sorted := sortedNodes(nodes)
	out := make([]admin.PredastoreNodeConfig, len(sorted))
	for i, n := range sorted {
		out[i] = admin.PredastoreNodeConfig{
			ID:   i + 1,
			Host: n.BindIP,
		}
	}
	return out
}

// sortedNodes returns nodes sorted by name.
func sortedNodes(nodes map[string]NodeInfo) []NodeInfo {
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)

	sorted := make([]NodeInfo, len(names))
	for i, name := range names {
		sorted[i] = nodes[name]
	}
	return sorted
}
