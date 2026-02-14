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
	"strings"
	"time"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var getCmd = &cobra.Command{
	Use:   "get",
	Short: "Display cluster resources",
	Long:  `Display cluster resources such as nodes and VMs.`,
}

var getNodesCmd = &cobra.Command{
	Use:   "nodes",
	Short: "Display cluster nodes",
	Long:  `Display all physical nodes in the cluster with status, IP, region, and services.`,
	Run:   runGetNodes,
}

var getVMsCmd = &cobra.Command{
	Use:     "vms",
	Aliases: []string{"instances"},
	Short:   "Display VMs across the cluster",
	Long:    `Display all VMs running across the cluster with instance type, resources, and placement.`,
	Run:     runGetVMs,
}

func init() {
	rootCmd.AddCommand(getCmd)
	getCmd.AddCommand(getNodesCmd)
	getCmd.AddCommand(getVMsCmd)

	getCmd.PersistentFlags().Duration("timeout", 3*time.Second, "Timeout for collecting responses from nodes")
}

// loadConfigAndConnect loads the cluster config and connects to NATS.
func loadConfigAndConnect() (*config.ClusterConfig, *nats.Conn, error) {
	cfgPath := viper.GetString("config")
	if cfgPath == "" {
		homeDir, _ := os.UserHomeDir()
		cfgPath = fmt.Sprintf("%s/hive/config/hive.toml", homeDir)
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}

	nodeConfig := cfg.Nodes[cfg.Node]
	nc, err := utils.ConnectNATS(nodeConfig.NATS.Host, nodeConfig.NATS.ACL.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	return cfg, nc, nil
}

// collectResponses publishes to a fan-out topic and collects all responses within the timeout.
func collectResponses(nc *nats.Conn, topic string, timeout time.Duration) ([][]byte, error) {
	inbox := nats.NewInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to inbox: %w", err)
	}
	defer sub.Unsubscribe()

	if err := nc.PublishRequest(topic, inbox, nil); err != nil {
		return nil, fmt.Errorf("failed to publish request: %w", err)
	}
	nc.Flush()

	var responses [][]byte
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		msg, err := sub.NextMsg(remaining)
		if err != nil {
			break // timeout or other error — done collecting
		}
		responses = append(responses, msg.Data)
	}
	return responses, nil
}

func formatUptime(seconds int64) string {
	if seconds <= 0 {
		return "-"
	}
	d := time.Duration(seconds) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func formatMemGB(gb float64) string {
	if gb >= 1.0 {
		return fmt.Sprintf("%.1fGi", gb)
	}
	return fmt.Sprintf("%dMi", int(gb*1024))
}

func runGetNodes(cmd *cobra.Command, args []string) {
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

	// Parse responses into a map by node name
	respondedNodes := make(map[string]config.NodeStatusResponse)
	for _, data := range responses {
		var resp config.NodeStatusResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		respondedNodes[resp.Node] = resp
	}

	// Build table: include all known nodes from config, mark non-responders as NotReady
	tableData := pterm.TableData{
		{"NAME", "STATUS", "IP", "REGION", "AZ", "UPTIME", "VMs", "SERVICES"},
	}

	// Collect and sort node names for stable output
	nodeNames := make([]string, 0, len(cfg.Nodes))
	for name := range cfg.Nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)

	for _, name := range nodeNames {
		nodeCfg := cfg.Nodes[name]
		if resp, ok := respondedNodes[name]; ok {
			tableData = append(tableData, []string{
				resp.Node,
				resp.Status,
				resp.Host,
				resp.Region,
				resp.AZ,
				formatUptime(resp.Uptime),
				fmt.Sprintf("%d", resp.VMCount),
				strings.Join(resp.Services, ","),
			})
		} else {
			tableData = append(tableData, []string{
				name,
				"NotReady",
				nodeCfg.Host,
				nodeCfg.Region,
				nodeCfg.AZ,
				"-",
				"-",
				strings.Join(nodeCfg.GetServices(), ","),
			})
		}
	}

	pterm.DefaultTable.WithHasHeader().WithLeftAlignment().WithData(tableData).Render()
}

func runGetVMs(cmd *cobra.Command, args []string) {
	_, nc, err := loadConfigAndConnect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer nc.Close()

	timeout, _ := cmd.Flags().GetDuration("timeout")
	responses, err := collectResponses(nc, "hive.node.vms", timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	type vmRow struct {
		config.VMInfo
		Node string
		Host string
	}

	var allVMs []vmRow
	for _, data := range responses {
		var resp config.NodeVMsResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		for _, v := range resp.VMs {
			allVMs = append(allVMs, vmRow{VMInfo: v, Node: resp.Node, Host: resp.Host})
		}
	}

	if len(allVMs) == 0 {
		fmt.Println("No VMs found.")
		return
	}

	// Sort by node then instance ID
	sort.Slice(allVMs, func(i, j int) bool {
		if allVMs[i].Node != allVMs[j].Node {
			return allVMs[i].Node < allVMs[j].Node
		}
		return allVMs[i].InstanceID < allVMs[j].InstanceID
	})

	tableData := pterm.TableData{
		{"INSTANCE", "STATUS", "TYPE", "VCPU", "MEM", "NODE", "IP", "AGE"},
	}

	for _, v := range allVMs {
		age := "-"
		if v.LaunchTime > 0 {
			age = formatUptime(time.Now().Unix() - v.LaunchTime)
		}
		tableData = append(tableData, []string{
			v.InstanceID,
			v.Status,
			v.InstanceType,
			fmt.Sprintf("%d", v.VCPU),
			formatMemGB(v.MemoryGB),
			v.Node,
			v.Host,
			age,
		})
	}

	pterm.DefaultTable.WithHasHeader().WithLeftAlignment().WithData(tableData).Render()
}
