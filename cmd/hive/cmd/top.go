/*
Copyright © 2025 Mulga Defense Corporation

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/mulgadc/hive/hive/config"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

var topCmd = &cobra.Command{
	Use:   "top",
	Short: "Display cluster resource usage",
	Long:  `Display resource usage (CPU, memory) for cluster nodes.`,
}

var topNodesCmd = &cobra.Command{
	Use:   "nodes",
	Short: "Display resource usage per node",
	Long:  `Display CPU and memory usage per node, plus a summary of available instance types across the cluster.`,
	Run:   runTopNodes,
}

func init() {
	rootCmd.AddCommand(topCmd)
	topCmd.AddCommand(topNodesCmd)

	topCmd.PersistentFlags().Duration("timeout", 3*time.Second, "Timeout for collecting responses from nodes")
}

func runTopNodes(cmd *cobra.Command, args []string) {
	cfg, nc, err := loadConfigAndConnect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer nc.Close()

	timeout, _ := cmd.Flags().GetDuration("timeout")
	responses, err := collectResponses(nc, "hive.node.status", timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	respondedNodes := make(map[string]config.NodeStatusResponse)
	for _, data := range responses {
		var resp config.NodeStatusResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		respondedNodes[resp.Node] = resp
	}

	// Node resource table
	nodeNames := make([]string, 0, len(cfg.Nodes))
	for name := range cfg.Nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)

	nodeTable := pterm.TableData{
		{"NAME", "CPU (used/total)", "MEM (used/total)", "VMs"},
	}

	// Aggregate instance type capacity across all nodes
	capacityMap := make(map[string]*aggregatedCap)

	for _, name := range nodeNames {
		if resp, ok := respondedNodes[name]; ok {
			nodeTable = append(nodeTable, []string{
				resp.Node,
				fmt.Sprintf("%d/%d", resp.AllocVCPU, resp.TotalVCPU),
				fmt.Sprintf("%s/%s", formatMemGB(resp.AllocMemGB), formatMemGB(resp.TotalMemGB)),
				fmt.Sprintf("%d", resp.VMCount),
			})

			for _, cap := range resp.InstanceTypes {
				if agg, ok := capacityMap[cap.Name]; ok {
					agg.Available += cap.Available
				} else {
					capacityMap[cap.Name] = &aggregatedCap{
						VCPU:      cap.VCPU,
						MemoryGB:  cap.MemoryGB,
						Available: cap.Available,
					}
				}
			}
		} else {
			nodeTable = append(nodeTable, []string{
				name,
				"-",
				"-",
				"-",
			})
		}
	}

	pterm.DefaultTable.WithHasHeader().WithLeftAlignment().WithData(nodeTable).Render()

	// Instance type capacity summary
	if len(capacityMap) == 0 {
		return
	}

	fmt.Println()

	capNames := make([]string, 0, len(capacityMap))
	for name := range capacityMap {
		capNames = append(capNames, name)
	}
	sort.Strings(capNames)

	capTable := pterm.TableData{
		{"INSTANCE TYPE", "AVAILABLE", "VCPU", "MEMORY"},
	}

	for _, name := range capNames {
		agg := capacityMap[name]
		capTable = append(capTable, []string{
			name,
			fmt.Sprintf("%d", agg.Available),
			fmt.Sprintf("%d", agg.VCPU),
			formatMemGB(agg.MemoryGB),
		})
	}

	pterm.DefaultTable.WithHasHeader().WithLeftAlignment().WithData(capTable).Render()
}

type aggregatedCap struct {
	VCPU      int
	MemoryGB  float64
	Available int
}
