package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mulgadc/spinifex/spinifex/admin"
	"github.com/mulgadc/spinifex/spinifex/config"
	"github.com/mulgadc/spinifex/spinifex/types"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var adminGpuCmd = &cobra.Command{
	Use:   "gpu",
	Short: "Manage GPU passthrough for a node",
}

var adminGpuStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show GPU hardware and passthrough state",
	Run:   runAdminGpuStatus,
}

var adminGpuEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable GPU passthrough on a node",
	Run:   runAdminGpuEnable,
}

var adminGpuDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable GPU passthrough on a node (blocked while GPU instances are running)",
	Run:   runAdminGpuDisable,
}

func init() {
	adminCmd.AddCommand(adminGpuCmd)
	adminGpuCmd.AddCommand(adminGpuStatusCmd)
	adminGpuCmd.AddCommand(adminGpuEnableCmd)
	adminGpuCmd.AddCommand(adminGpuDisableCmd)

	adminGpuStatusCmd.Flags().String("node", "", "Target node name (default: local node)")
	adminGpuEnableCmd.Flags().String("node", "", "Target node name (default: local node)")
	adminGpuDisableCmd.Flags().String("node", "", "Target node name (default: local node)")
}

// gpuNodeStatus queries NATS and returns the NodeStatusResponse for the target node.
func gpuNodeStatus(targetNode string) (*types.NodeStatusResponse, error) {
	cfg, nc, err := loadConfigAndConnect()
	if err != nil {
		return nil, err
	}
	defer nc.Close()

	if targetNode == "" {
		targetNode = cfg.Node
	}

	responses, err := collectResponses(nc, "spinifex.node.status", 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("collect node status: %w", err)
	}
	for _, raw := range responses {
		var resp types.NodeStatusResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			continue
		}
		if resp.Node == targetNode {
			return &resp, nil
		}
	}
	return nil, fmt.Errorf("node %q not found or not responding", targetNode)
}

func runAdminGpuStatus(cmd *cobra.Command, _ []string) {
	targetNode, _ := cmd.Flags().GetString("node")
	resp, err := gpuNodeStatus(targetNode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	iommu := "unknown"
	vfio := "unknown"
	if resp.GPUCapable || len(resp.GPUModels) > 0 {
		iommu = "active"
		vfio = "loaded"
	}

	fmt.Printf("Node:            %s\n", resp.Node)
	if len(resp.GPUModels) > 0 {
		fmt.Printf("GPU hardware:    %s\n", strings.Join(resp.GPUModels, ", "))
	} else {
		fmt.Printf("GPU hardware:    none detected\n")
	}
	fmt.Printf("IOMMU:           %s\n", iommu)
	fmt.Printf("vfio-pci:        %s\n", vfio)

	if resp.GPUPassthrough {
		fmt.Printf("Passthrough:     enabled\n")
		fmt.Printf("GPU pool:        %d/%d allocated\n", resp.AllocGPUs, resp.TotalGPUs)

		var g5Types []string
		for _, cap := range resp.InstanceTypes {
			if strings.HasPrefix(cap.Name, "g5.") {
				g5Types = append(g5Types, cap.Name)
			}
		}
		if len(g5Types) > 0 {
			fmt.Printf("Instance types:  %s\n", strings.Join(g5Types, " "))
		}
	} else if resp.GPUCapable {
		fmt.Printf("Passthrough:     disabled\n")
		fmt.Printf("Instance types:  (none — run 'spx admin gpu enable' to activate g5.*)\n")
	} else {
		fmt.Printf("Passthrough:     disabled\n")
		fmt.Printf("Instance types:  (prerequisites not met)\n")
	}
}

// gpuToggle writes the TOML setting, sends SIGHUP, and polls until the daemon confirms the new state.
func gpuToggle(cmd *cobra.Command, enable bool) {
	targetNode, _ := cmd.Flags().GetString("node")

	cfgPath := viper.GetString("config")
	if cfgPath == "" {
		cfgPath = DefaultConfigFile()
	}
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	if targetNode == "" {
		targetNode = cfg.Node
	}

	// Check current state first.
	resp, err := gpuNodeStatus(targetNode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if enable {
		if resp.GPUPassthrough {
			fmt.Println("GPU passthrough is already enabled.")
			return
		}
		if !resp.GPUCapable {
			fmt.Fprintln(os.Stderr, "Error: prerequisites not met on this node.")
			if !resp.GPUCapable {
				fmt.Fprintln(os.Stderr, "  Run 'sudo spx-test-gpu' to diagnose and configure the host.")
			}
			os.Exit(1)
		}
	} else {
		if !resp.GPUPassthrough {
			fmt.Println("GPU passthrough is already disabled.")
			return
		}
		if resp.AllocGPUs > 0 {
			fmt.Fprintf(os.Stderr, "Error: %d GPU instance(s) are running. Terminate them first.\n", resp.AllocGPUs)
			os.Exit(1)
		}
	}

	// Write the TOML setting.
	if err := admin.SetGPUPassthrough(cfgPath, targetNode, enable); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing config: %v\n", err)
		os.Exit(1)
	}

	// Signal the daemon to reload.
	out, err := exec.Command("systemctl", "kill", "-s", "HUP", "spinifex-daemon").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error sending SIGHUP: %v\n%s\n", err, out)
		os.Exit(1)
	}

	// Poll until the daemon confirms the new state.
	action := "enable"
	if !enable {
		action = "disable"
	}
	fmt.Printf("Waiting for daemon to %s GPU passthrough", action)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(1 * time.Second)
		fmt.Print(".")
		r, err := gpuNodeStatus(targetNode)
		if err != nil {
			continue
		}
		if r.GPUPassthrough == enable {
			fmt.Println(" done.")
			runAdminGpuStatus(cmd, nil)
			return
		}
	}
	fmt.Println()
	fmt.Fprintln(os.Stderr, "Timed out waiting for daemon. Check: journalctl -u spinifex-daemon -n 50")
	os.Exit(1)
}

func runAdminGpuEnable(cmd *cobra.Command, _ []string) {
	gpuToggle(cmd, true)
}

func runAdminGpuDisable(cmd *cobra.Command, _ []string) {
	gpuToggle(cmd, false)
}
