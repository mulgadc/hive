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
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/mulgadc/spinifex/spinifex/admin"
	"github.com/mulgadc/spinifex/spinifex/config"
	"github.com/mulgadc/spinifex/spinifex/formation"
	handlers_ec2_vpc "github.com/mulgadc/spinifex/spinifex/handlers/ec2/vpc"
	handlers_iam "github.com/mulgadc/spinifex/spinifex/handlers/iam"
	"github.com/mulgadc/spinifex/spinifex/utils"
	"github.com/mulgadc/viperblock/viperblock"
	"github.com/mulgadc/viperblock/viperblock/backends/s3"
	"github.com/mulgadc/viperblock/viperblock/v_utils"
	"github.com/nats-io/nats.go"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

//go:embed templates/spinifex.toml
var spinifexTomlTemplate string

//go:embed templates/awsgw.toml
var awsgwTomlTemplate string

//go:embed templates/predastore.toml
var predastoreTomlTemplate string

//go:embed templates/nats.conf
var natsConfTemplate string

//go:embed templates/predastore-multinode.toml
var predastoreMultiNodeTemplate string

var supportedArchs = map[string]bool{
	"x86_64":  true,
	"aarch64": true, // alias for arm64
	"arm64":   true,
}

// TODO: Confirm suppported platform types
var supportedPlatforms = map[string]bool{
	"Linux/UNIX": true,
	"Windows":    true,
}

var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Administrative commands for Spinifex platform management",
	Long:  `Administrative commands for initializing and managing the Spinifex platform.`,
}

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Cluster-wide operations",
	Long:  `Cluster-wide administrative operations such as coordinated shutdown.`,
}

var clusterShutdownCmd = &cobra.Command{
	Use:   "shutdown",
	Short: "Gracefully shut down the entire cluster",
	Long: `Perform a coordinated, phased shutdown of the entire cluster.
Phases execute in order: GATE (stop API/UI) → DRAIN (stop VMs) → STORAGE (stop viperblock) → PERSIST (stop predastore) → INFRA (stop NATS/daemon).
Each phase waits for all nodes to ACK before proceeding to the next.`,
	Run: runClusterShutdown,
}

var adminInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Spinifex platform configuration",
	Long: `Initialize Spinifex platform by creating configuration files, generating SSL certificates,
and setting up AWS credentials. This creates the necessary directory structure and
configuration files in ~/spinifex/config.`,
	Run: runAdminInit,
}

var adminJoinCmd = &cobra.Command{
	Use:   "join",
	Short: "Join an existing Spinifex cluster",
	Long: `Join an existing Spinifex cluster by connecting to a leader node and retrieving
the cluster configuration. This command will configure the local node to join
the cluster and participate in distributed operations.`,
	Run: runAdminJoin,
}

var imagesCmd = &cobra.Command{
	Use:   "images",
	Short: "Manage OS images",
	Long:  `Manage OS images for local storage and AMI creation.`,
}

var imagesImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Specify local file to import",
	Long:  `Create a new image from a local file`,
	Run:   runimagesImportCmd,
}

var imagesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List OS images to import or download",
	Long:  `Query the remote endpoint for common OS images available for import as AMI or locally download.`,
	Run:   runimagesListCmd,
}

var accountCmd = &cobra.Command{
	Use:   "account",
	Short: "Manage Spinifex accounts",
	Long:  `Create and manage Spinifex accounts. Each account namespaces IAM users, policies, and resources.`,
}

var accountCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new account with an admin user",
	Long: `Create a new Spinifex account. This creates an account with a sequential 12-digit ID,
an admin user, and an AdministratorAccess policy attached to the admin user.
Requires the cluster to be running (connects to NATS).`,
	Run: runAccountCreate,
}

var accountListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all accounts",
	Long:  `List all Spinifex accounts with their ID, name, status, and creation time.`,
	Run:   runAccountList,
}

/*
CLI ideas

spx admin images list

- fetches from remote endpoint for common/trusted images to bootstrap environment, or baked in from compile.

// If --name specified, download
spx admin images import --name debian-12-x86_64

// List available images
spx admin images list

// Manually import a path
spx admin images import --file /path/to/image --distro debian --version 12 --arch x86_64

-> x <-
*/

func init() {
	rootCmd.AddCommand(adminCmd)
	adminCmd.AddCommand(adminInitCmd)
	adminCmd.AddCommand(adminJoinCmd)

	adminCmd.AddCommand(clusterCmd)
	clusterCmd.AddCommand(clusterShutdownCmd)
	clusterShutdownCmd.Flags().Bool("force", false, "Force shutdown even if nodes don't respond")
	clusterShutdownCmd.Flags().Duration("timeout", 120*time.Second, "Maximum time to wait per phase")
	clusterShutdownCmd.Flags().Bool("dry-run", false, "Print phase plan without executing")

	adminCmd.AddCommand(imagesCmd)
	imagesCmd.AddCommand(imagesImportCmd)
	imagesCmd.AddCommand(imagesListCmd)

	adminCmd.AddCommand(accountCmd)
	accountCmd.AddCommand(accountCreateCmd)
	accountCmd.AddCommand(accountListCmd)
	accountCreateCmd.Flags().String("name", "", "Account name (required)")
	accountCreateCmd.MarkFlagRequired("name")

	rootCmd.PersistentFlags().String("config-dir", DefaultConfigDir(), "Configuration directory")
	rootCmd.PersistentFlags().String("spinifex-dir", DefaultDataDir(), "Spinifex base directory")

	// Flags for admin init
	adminInitCmd.Flags().Bool("force", false, "Force re-initialization (overwrites existing config)")
	adminInitCmd.Flags().String("region", "ap-southeast-2", "Mulga region to create")
	adminInitCmd.Flags().String("az", "ap-southeast-2a", "Mulga AZ to create")
	adminInitCmd.Flags().String("node", "node1", "Node name, increment for additional nodes (default, node1)")
	adminInitCmd.Flags().Int("nodes", 3, "Number of nodes to expect for cluster")
	adminInitCmd.Flags().String("host", "", "Leader node to join (if not specified, tries multicast discovery)")
	adminInitCmd.Flags().Int("port", 4432, "Port to bind cluster services on")
	adminInitCmd.Flags().String("bind", "0.0.0.0", "IP address to bind services to (e.g., 10.11.12.1 for multi-node)")
	adminInitCmd.Flags().String("cluster-bind", "", "IP address to bind NATS cluster services to (e.g., 10.11.12.1 for multi-node)")
	adminInitCmd.Flags().String("cluster-routes", "", "NATS cluster hosts for routing specify multiple with comma (e.g., 10.11.12.1:4248,10.11.12.2:4248 for multi-node)")
	adminInitCmd.Flags().String("predastore-nodes", "", "Comma-separated IPs for multi-node Predastore cluster (e.g., 10.11.12.1,10.11.12.2,10.11.12.3). Requires >= 3 nodes.")
	adminInitCmd.Flags().String("formation-timeout", "10m", "Timeout for cluster formation (e.g., 5m, 30s)")
	adminInitCmd.Flags().String("cluster-name", "spinifex", "NATS cluster name")
	adminInitCmd.Flags().StringSlice("services", nil, "Services this node runs (default: all). Valid: nats,predastore,viperblock,daemon,awsgw,ui")

	// Flags for admin join
	adminJoinCmd.Flags().String("region", "ap-southeast-2", "Region for this node")
	adminJoinCmd.Flags().String("az", "ap-southeast-2a", "Availability zone for this node")
	adminJoinCmd.Flags().String("node", "", "Node name (required)")
	adminJoinCmd.Flags().String("host", "", "Leader node host:port (e.g., node1.local:4432) (required)")
	adminJoinCmd.Flags().String("data-dir", "", "Data directory for this node (default: ~/spinifex)")
	adminJoinCmd.Flags().Int("port", 4432, "Port to bind cluster services on")
	adminJoinCmd.Flags().String("bind", "0.0.0.0", "IP address to bind services to (e.g., 10.11.12.2 for multi-node on single host)")
	adminJoinCmd.Flags().String("cluster-bind", "", "IP address to bind NATS cluster services to (e.g., 10.11.12.1 for multi-node)")
	adminJoinCmd.Flags().String("cluster-routes", "", "NATS cluster hosts for routing specify multiple with comma (e.g., 10.11.12.1:4248,10.11.12.2:4248 for multi-node)")
	adminJoinCmd.Flags().StringSlice("services", nil, "Services this node runs (default: all)")
	adminJoinCmd.MarkFlagRequired("node")
	adminJoinCmd.MarkFlagRequired("host")

	imagesImportCmd.Flags().String("tmp-dir", os.TempDir(), "Temporary directory for image import processing")

	imagesImportCmd.Flags().String("name", "", "Import specified image by name")
	imagesImportCmd.Flags().String("file", "", "Import file from specified path (raw, qcow2, compressed)")
	imagesImportCmd.Flags().String("distro", "", "Specified distro name (e.g debian)")
	imagesImportCmd.Flags().String("version", "", "Specified distro version (e.g 12)")
	imagesImportCmd.Flags().String("arch", "", "Specified distro arch (e.g aarch64, arm64, x86_64)")
	imagesImportCmd.Flags().String("platform", "Linux/UNIX", "Specified platform (e.g Linux/UNIX, Windows)")
	imagesImportCmd.Flags().Bool("force", false, "Force command execution (overwrites existing files)")
}

func runimagesImportCmd(cmd *cobra.Command, args []string) {
	var image utils.Images

	var imageFile string
	var imageStat os.FileInfo
	var err error

	cfgFile, _ := cmd.Flags().GetString("config")
	forceCmd, _ := cmd.Flags().GetBool("force")
	ostmpDir, _ := cmd.Flags().GetString("tmp-dir")

	// Use default config path
	if cfgFile == "" {
		cfgFile = DefaultConfigFile()
	}

	//configDir, _ := cmd.Flags().GetString("config-dir")
	baseDir, _ := cmd.Flags().GetString("spinifex-dir")

	// Strip trailing slash
	baseDir = filepath.Clean(baseDir)

	// Check the base dir has our images path, and correctlty init
	imageDir := fmt.Sprintf("%s/images", baseDir)

	if !admin.FileExists(imageDir) {
		fmt.Fprintf(os.Stderr, "Error: image directory does not exist: %s\n\n", imageDir)
		fmt.Fprintf(os.Stderr, "Run 'spx admin init' first to initialize the Spinifex platform.\n")
		os.Exit(1)
	}

	// Determine, if name specified, or file
	imageName, _ := cmd.Flags().GetString("name")

	if imageName != "" {
		var exists bool
		// Confirm the image exists
		image, exists = utils.AvailableImages[imageName]

		if !exists {
			fmt.Fprintf(os.Stderr, "Image name not found in available images")
			os.Exit(1)
		}
	} else {
		imageFile, _ = cmd.Flags().GetString("file")

		if imageFile == "" {
			fmt.Fprintf(os.Stderr, "File required to import image")
			os.Exit(1)
		}

		if _, err := os.Stat(imageFile); err != nil {
			fmt.Fprintf(os.Stderr, "File could not be found: %s", err)
			os.Exit(1)
		}

		image.Distro, _ = cmd.Flags().GetString("distro")
		image.Version, _ = cmd.Flags().GetString("version")
		image.Arch, _ = cmd.Flags().GetString("arch")
		image.Platform, _ = cmd.Flags().GetString("platform")
	}

	if image.Distro == "" {
		fmt.Fprintf(os.Stderr, "Specify distro name")
		os.Exit(1)
	}

	// Check version specified
	if image.Version == "" {
		fmt.Fprintf(os.Stderr, "Specify image version")
		os.Exit(1)
	}

	if !supportedArchs[image.Arch] {
		fmt.Fprintf(os.Stderr, "Unsupported architecture")
		os.Exit(1)
	}

	if !supportedPlatforms[image.Platform] {
		fmt.Fprintf(os.Stderr, "Unsupported platform")
		os.Exit(1)
	}

	// Create the specified image directory
	imagePath := fmt.Sprintf("%s/%s/%s/%s", imageDir, image.Distro, image.Version, image.Arch)

	// Create config directory
	if err := os.MkdirAll(imagePath, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating config directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Created config directory: %s\n", imagePath)

	// Next, if the file is selected to download, fetch it, extract disk image, and save to path
	if imageName != "" {
		// Download the file to the image path
		filename := path.Base(image.URL)
		imageFile = fmt.Sprintf("%s/%s", imagePath, filename)

		fmt.Printf("Downloading image %s to %s\n", image.URL, imageFile)

		// If image path exists, skip
		if admin.FileExists(imageFile) && !forceCmd {
			fmt.Printf("Image file already exists, skipping download, use --force to overwrite: %s\n", imageFile)
		} else {
			err := utils.DownloadFileWithProgress(image.URL, image.Name, imageFile, 0)

			if err != nil {
				fmt.Printf("Download failed: %v\n", err)
				os.Exit(1)
			}
		}

		// Update image file path for later extraction
		//imagePath = imageFilePath
	}

	// Next, validate if the image is raw, tar, gz, xv, etc. We need to upload the raw image
	tmpDir, err := os.MkdirTemp(ostmpDir, "spinifex-image-tmp-*")

	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not create temp dir: %v\n", err)
		os.Exit(1)
	}

	extractedImagePath, err := utils.ExtractDiskImageFromFile(imageFile, imagePath)

	fmt.Println("Extracted image to:", extractedImagePath)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not extract image: %v\n", err)
		os.Exit(1)
	}

	imageStat, err = os.Stat(extractedImagePath)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not stat image: %v\n", err)
		os.Exit(1)
	}

	// Create the specified manifest file to describe the image/AMI
	manifest := viperblock.VolumeConfig{}

	// Calculate the size

	manifest.AMIMetadata.Name = fmt.Sprintf("ami-%s-%s-%s", image.Distro, image.Version, image.Arch)
	volumeId := utils.GenerateResourceID("ami")
	manifest.AMIMetadata.ImageID = volumeId

	manifest.AMIMetadata.Description = fmt.Sprintf("%s cloud image prepared for Spinifex", manifest.AMIMetadata.Name)
	manifest.AMIMetadata.Architecture = image.Arch
	manifest.AMIMetadata.PlatformDetails = image.Platform
	manifest.AMIMetadata.CreationDate = time.Now()
	manifest.AMIMetadata.RootDeviceType = "ebs"
	manifest.AMIMetadata.Virtualization = "hvm"
	manifest.AMIMetadata.ImageOwnerAlias = "system"
	manifest.AMIMetadata.VolumeSizeGiB = utils.SafeInt64ToUint64(imageStat.Size() / 1024 / 1024 / 1024)

	// Volume Data
	manifest.VolumeMetadata.VolumeID = volumeId // TODO: Confirm if unique, e.g vol-, if ami- used
	manifest.VolumeMetadata.VolumeName = manifest.AMIMetadata.Name
	manifest.VolumeMetadata.TenantID = "system"
	manifest.VolumeMetadata.SizeGiB = manifest.AMIMetadata.VolumeSizeGiB
	manifest.VolumeMetadata.State = "available"
	manifest.VolumeMetadata.AvailabilityZone = "" // TODO: Confirm
	manifest.VolumeMetadata.CreatedAt = time.Now()
	manifest.VolumeMetadata.VolumeType = "gp3"
	manifest.VolumeMetadata.IOPS = 1000

	// Write the manifest to disk
	// Save as JSON
	jsonData, err := json.Marshal(manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not marshal manifest: %v\n", err)
		os.Exit(1)
	}

	manifestFilename := fmt.Sprintf("%s/%s.json", imagePath, manifest.AMIMetadata.Name)
	// Write to file
	err = os.WriteFile(manifestFilename, jsonData, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not write manifest: %v\n", err)
		os.Exit(1)
	}

	// Upload the image to S3 (predastore)

	appConfig, err := config.LoadConfig(cfgFile)

	if err != nil {
		fmt.Println("Error loading config file:", err)
		return
	}

	s3Config := s3.S3Config{
		VolumeName: volumeId,
		VolumeSize: utils.SafeInt64ToUint64(imageStat.Size()),
		Bucket:     appConfig.Nodes[appConfig.Node].Predastore.Bucket,
		Region:     appConfig.Nodes[appConfig.Node].Predastore.Region,
		AccessKey:  appConfig.Nodes[appConfig.Node].Predastore.AccessKey,
		SecretKey:  appConfig.Nodes[appConfig.Node].Predastore.SecretKey,
		Host:       appConfig.Nodes[appConfig.Node].Predastore.Host,
	}

	vbConfig := viperblock.VB{
		VolumeName: volumeId,
		VolumeSize: utils.SafeInt64ToUint64(imageStat.Size()),
		BaseDir:    tmpDir,
		Cache: viperblock.Cache{
			Config: viperblock.CacheConfig{
				Size: 0,
			},
		},
		VolumeConfig: manifest,
	}

	err = v_utils.ImportDiskImage(&s3Config, &vbConfig, extractedImagePath)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not import image to predastore: %v\n", err)
		os.Exit(1)
	}

	defer os.RemoveAll(tmpDir)

	fmt.Printf("✅ Image import complete. Image-ID (AMI): %s\n", volumeId)
}

// List remote images available
func runimagesListCmd(cmd *cobra.Command, args []string) {
	//fmt.Println(availableImages)

	tableData := pterm.TableData{
		{"NAME", "DISTRO", "VERSION", "ARCH", "BOOT"},
	}

	// Sort A .. Z
	// 1. Collect keys
	keys := make([]string, 0, len(utils.AvailableImages))
	for k := range utils.AvailableImages {
		keys = append(keys, k)
	}

	// 2. Sort keys alphabetically (A→Z)
	sort.Strings(keys)

	// 3. Iterate in sorted order
	for _, k := range keys {
		img := utils.AvailableImages[k]

		//for _, img := range utils.AvailableImages {
		tableData = append(tableData, []string{img.Name, img.Distro, img.Version, img.Arch, img.BootMode})
	}

	// Create a table with the defined data.
	// The table has a header and the text in the cells is right-aligned.
	// The Render() method is used to print the table to the console.
	pterm.DefaultTable.WithHasHeader().WithLeftAlignment().WithData(tableData).Render()

	pterm.Println("To install a selected image as an AMI use:")

	pterm.Println("spx admin images import --name <image-name>")
}

// TODO: Move all logic to a module, use minimal application logic in viper commands
func runAdminInit(cmd *cobra.Command, args []string) {
	force, _ := cmd.Flags().GetBool("force")
	configDir, _ := cmd.Flags().GetString("config-dir")
	spxRoot, _ := cmd.Flags().GetString("spinifex-dir")
	region, _ := cmd.Flags().GetString("region")
	az, _ := cmd.Flags().GetString("az")
	node, _ := cmd.Flags().GetString("node")
	nodes, _ := cmd.Flags().GetInt("nodes")
	port, _ := cmd.Flags().GetInt("port")
	bindIP, _ := cmd.Flags().GetString("bind")
	clusterBind, _ := cmd.Flags().GetString("cluster-bind")
	clusterRoutesStr, _ := cmd.Flags().GetString("cluster-routes")
	var clusterRoutes []string
	if clusterRoutesStr != "" {
		clusterRoutes = strings.Split(clusterRoutesStr, ",")
	}
	predastoreNodesStr, _ := cmd.Flags().GetString("predastore-nodes")
	formationTimeoutStr, _ := cmd.Flags().GetString("formation-timeout")
	clusterName, _ := cmd.Flags().GetString("cluster-name")
	services, _ := cmd.Flags().GetStringSlice("services")

	// Validate IP address format
	if net.ParseIP(bindIP) == nil {
		fmt.Fprintf(os.Stderr, "❌ Error: Invalid IP address for --bind: %s\n", bindIP)
		os.Exit(1)
	}

	// Validate port range
	if port < 1 || port > 65535 {
		fmt.Fprintf(os.Stderr, "❌ Error: Port must be between 1 and 65535, got: %d\n", port)
		os.Exit(1)
	}

	// Default cluster-bind to bind IP if not specified
	if clusterBind == "" {
		clusterBind = bindIP
	}

	fmt.Printf("Initializing Spinifex with bind IP: %s, port: %d\n", bindIP, port)

	// Default config directory
	if configDir == "" {
		configDir = DefaultConfigDir()
	}

	fmt.Println("🚀 Initializing Spinifex platform...")
	fmt.Printf("Configuration directory: %s\n\n", configDir)

	// Check if already initialized
	spinifexTomlPath := filepath.Join(configDir, "spinifex.toml")
	if !force && admin.FileExists(spinifexTomlPath) {
		fmt.Println("⚠️  Spinifex already initialized!")
		fmt.Printf("Config file exists: %s\n", spinifexTomlPath)
		fmt.Println("\nTo re-initialize, run with --force flag:")
		fmt.Println("  spx admin init --force")
		os.Exit(0)
	}

	// Create config directory
	if err := os.MkdirAll(configDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating config directory: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Created config directory: %s\n", configDir)

	// Generate system credentials (for service-to-service auth in config files)
	accessKey, err := admin.GenerateAWSAccessKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating access key: %v\n", err)
		os.Exit(1)
	}
	secretKey, err := admin.GenerateAWSSecretKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating secret key: %v\n", err)
		os.Exit(1)
	}
	accountID := admin.SystemAccountID()

	// Generate IAM master key (AES-256, used to encrypt secrets in NATS KV)
	masterKey, err := handlers_iam.GenerateMasterKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating IAM master key: %v\n", err)
		os.Exit(1)
	}
	bootstrapResult, err := writeBootstrapFiles(configDir, masterKey, accessKey, secretKey, accountID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing bootstrap files: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\n🔐 Generated IAM master key")
	fmt.Printf("   Master key: %s\n", filepath.Join(configDir, "master.key"))
	fmt.Printf("   Bootstrap: %s\n", filepath.Join(configDir, "bootstrap.json"))

	fmt.Printf("\n🔑 Generated admin credentials (save these — they won't be shown again):\n")
	fmt.Printf("   Access Key:  %s\n", bootstrapResult.AdminAccessKey)
	fmt.Printf("   Secret Key:  %s\n", bootstrapResult.AdminSecretKey)
	fmt.Printf("   Account:     %s (%s)\n", admin.DefaultAccountName(), admin.DefaultAccountID())
	fmt.Printf("   AWS Profile: spinifex\n")

	// Generate SSL certificates (with bind IP in SANs for multi-node support)
	certPath := admin.GenerateCertificatesIfNeeded(configDir, force, bindIP)

	// Generate NATS token
	natsToken, err := admin.GenerateNATSToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating NATS token: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\n🔒 Generated NATS authentication token")

	if spxRoot == "" {
		spxRoot = DefaultDataDir()
	}
	spxRoot = filepath.Clean(spxRoot)

	// Determine if this is a multi-node formation
	isMultiNode := nodes >= 2 && bindIP != "0.0.0.0"

	if isMultiNode {
		runAdminInitMultiNode(cmd, accessKey, secretKey, accountID, natsToken, clusterName,
			configDir, spxRoot, certPath, region, az, node, bindIP, clusterBind,
			port, nodes, formationTimeoutStr, services)
		return
	}

	// --- Single-node path (existing behavior) ---

	// Create config files from embedded templates
	fmt.Println("\n📝 Creating configuration files...")

	// Create subdirectories
	awsgwDir := filepath.Join(configDir, "awsgw")
	predastoreDir := filepath.Join(configDir, "predastore")
	natsDir := filepath.Join(configDir, "nats")
	spinifexDir := filepath.Join(configDir, "spinifex")

	for _, dir := range []string{awsgwDir, predastoreDir, natsDir, spinifexDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory %s: %v\n", dir, err)
			os.Exit(1)
		}
	}

	portStr := fmt.Sprintf("%d", port)

	// Parse multi-node predastore configuration (legacy flag-based approach for single-node)
	var predastoreNodeID int
	if predastoreNodesStr != "" {
		ips := strings.Split(predastoreNodesStr, ",")
		if len(ips) < 3 {
			fmt.Fprintf(os.Stderr, "❌ Error: --predastore-nodes requires at least 3 IPs, got %d\n", len(ips))
			os.Exit(1)
		}

		var predastoreNodes []admin.PredastoreNodeConfig
		for i, ip := range ips {
			ip = strings.TrimSpace(ip)
			if net.ParseIP(ip) == nil {
				fmt.Fprintf(os.Stderr, "❌ Error: Invalid IP in --predastore-nodes: %s\n", ip)
				os.Exit(1)
			}
			predastoreNodes = append(predastoreNodes, admin.PredastoreNodeConfig{
				ID:   i + 1,
				Host: ip,
			})
		}

		predastoreNodeID = admin.FindNodeIDByIP(predastoreNodes, bindIP)
		if predastoreNodeID == 0 {
			fmt.Fprintf(os.Stderr, "❌ Error: --bind IP %s not found in --predastore-nodes list\n", bindIP)
			os.Exit(1)
		}

		// Generate multi-node predastore.toml
		predastoreContent, err := admin.GenerateMultiNodePredastoreConfig(predastoreMultiNodeTemplate, predastoreNodes, accessKey, secretKey, region, natsToken, configDir, bindIP)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating multi-node predastore config: %v\n", err)
			os.Exit(1)
		}

		predastorePath := filepath.Join(predastoreDir, "predastore.toml")
		if err := os.WriteFile(predastorePath, []byte(predastoreContent), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing predastore config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Created: multi-node predastore.toml (node ID: %d)\n", predastoreNodeID)
	}

	configSettings := admin.ConfigSettings{
		AccessKey: accessKey,
		SecretKey: secretKey,
		AccountID: accountID,
		Region:    region,
		NatsToken: natsToken,
		DataDir:   spxRoot,
		ConfigDir: configDir,

		Node:          node,
		Az:            az,
		Port:          portStr,
		BindIP:        bindIP,
		ClusterBindIP: clusterBind,
		ClusterRoutes: clusterRoutes,
		ClusterName:   clusterName,

		PredastoreNodeID: predastoreNodeID,
		Services:         services,

		OVNNBAddr: "tcp:127.0.0.1:6641",
		OVNSBAddr: "tcp:127.0.0.1:6642",
	}

	// Generate config files
	configs := []admin.ConfigFile{
		{Name: "spinifex.toml", Path: spinifexTomlPath, Template: spinifexTomlTemplate},
		{Name: filepath.Join(awsgwDir, "awsgw.toml"), Path: filepath.Join(awsgwDir, "awsgw.toml"), Template: awsgwTomlTemplate},
		{Name: filepath.Join(natsDir, "nats.conf"), Path: filepath.Join(natsDir, "nats.conf"), Template: natsConfTemplate},
	}
	// Skip template-based predastore.toml if multi-node was already generated
	if predastoreNodesStr == "" {
		configs = append(configs, admin.ConfigFile{
			Name: filepath.Join(predastoreDir, "predastore.toml"), Path: filepath.Join(predastoreDir, "predastore.toml"), Template: predastoreTomlTemplate,
		})
	}

	if err := admin.GenerateConfigFiles(configs, configSettings); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating configuration files: %v\n", err)
		os.Exit(1)
	}

	// Update ~/.aws/credentials and ~/.aws/config with admin credentials (not system)
	fmt.Println("\n🔧 Configuring AWS credentials...")
	if err := admin.SetupAWSCredentials(bootstrapResult.AdminAccessKey, bootstrapResult.AdminSecretKey, region, certPath, bindIP); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not update AWS credentials: %v\n", err)
	} else {
		fmt.Println("✅ AWS credentials configured")
	}

	admin.CreateServiceDirectories(spxRoot)

	// In production layout (running as root), fix ownership so the service user
	// can read config and write data. SUDO_USER identifies the operator account.
	if os.Getuid() == 0 {
		sudoUser := os.Getenv("SUDO_USER")
		if sudoUser != "" {
			admin.ChownRecursive(configDir, sudoUser)
			admin.ChownRecursive(spxRoot, sudoUser)
		}
	}

	// Print success message
	fmt.Println("\n🎉 Spinifex initialization complete!")
	fmt.Println("\n📋 Next steps:")
	fmt.Println("   1. Start services:")
	fmt.Println("      ./scripts/start-dev.sh")
	fmt.Println()
	fmt.Println("   2. Test with AWS CLI:")
	fmt.Println("      export AWS_PROFILE=spinifex")
	fmt.Println("      aws ec2 describe-instances")
	fmt.Println()
	fmt.Println("🔗 Configuration:")
	fmt.Printf("   Config file: %s\n", spinifexTomlPath)
	fmt.Printf("   Data directory: %s\n", spxRoot)
	fmt.Println()
}

// runAdminInitMultiNode handles the multi-node formation path for admin init.
// It starts a formation server, registers this node, waits for all nodes to join,
// then generates configs with complete cluster topology.
func runAdminInitMultiNode(cmd *cobra.Command, accessKey, secretKey, accountID, natsToken, clusterName,
	configDir, spxRoot, certPath, region, az, node, bindIP, clusterBind string,
	port, expectedNodes int, formationTimeoutStr string, services []string) {
	formationTimeout, err := time.ParseDuration(formationTimeoutStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: Invalid --formation-timeout: %v\n", err)
		os.Exit(1)
	}

	// Generate IAM master key for the cluster
	masterKey, err := handlers_iam.GenerateMasterKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error generating IAM master key: %v\n", err)
		os.Exit(1)
	}
	bootstrapResult, err := writeBootstrapFiles(configDir, masterKey, accessKey, secretKey, accountID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error writing bootstrap files: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\n🔐 Generated IAM master key")
	fmt.Printf("   Bootstrap: %s\n", filepath.Join(configDir, "bootstrap.json"))

	// Read CA cert/key for distribution to joining nodes
	caCertData, err := os.ReadFile(filepath.Join(configDir, "ca.pem"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error reading CA cert: %v\n", err)
		os.Exit(1)
	}
	caKeyData, err := os.ReadFile(filepath.Join(configDir, "ca.key"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error reading CA key: %v\n", err)
		os.Exit(1)
	}

	creds := &formation.SharedCredentials{
		AccessKey:      accessKey,
		SecretKey:      secretKey,
		AccountID:      accountID,
		NatsToken:      natsToken,
		ClusterName:    clusterName,
		Region:         region,
		AdminAccessKey: bootstrapResult.AdminAccessKey,
		AdminSecretKey: bootstrapResult.AdminSecretKey,
	}

	fs := formation.NewFormationServer(expectedNodes, creds, string(caCertData), string(caKeyData))

	// Include master key in formation server for distribution to joining nodes
	fs.SetMasterKey(base64.StdEncoding.EncodeToString(masterKey))

	// Register self (init node) as the first node
	selfNode := formation.NodeInfo{
		Name:      node,
		BindIP:    bindIP,
		ClusterIP: clusterBind,
		Region:    region,
		AZ:        az,
		Port:      port,
	}
	if err := fs.RegisterNode(selfNode); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error registering self: %v\n", err)
		os.Exit(1)
	}

	// Start formation server
	formationAddr := fmt.Sprintf("%s:%d", bindIP, port)
	if err := fs.Start(formationAddr); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error starting formation server: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n📡 Formation server started on %s\n", formationAddr)
	fmt.Printf("   Waiting for %d more node(s) to join...\n", expectedNodes-1)
	fmt.Printf("   Other nodes should run: spx admin join --host %s --node <name> --bind <ip>\n\n", formationAddr)

	// Wait for all nodes to register
	if err := fs.WaitForCompletion(formationTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		fs.Shutdown(context.Background())
		os.Exit(1)
	}

	fmt.Printf("✅ All %d nodes joined!\n", expectedNodes)

	// Build cluster topology from formation data
	allNodes := fs.Nodes()
	clusterRoutes := formation.BuildClusterRoutes(allNodes)
	predastoreNodes := formation.BuildPredastoreNodes(allNodes)

	fmt.Println("\n📝 Creating configuration files...")

	// Create subdirectories
	awsgwDir := filepath.Join(configDir, "awsgw")
	predastoreDir := filepath.Join(configDir, "predastore")
	natsDir := filepath.Join(configDir, "nats")
	spinifexDir := filepath.Join(configDir, "spinifex")

	for _, dir := range []string{awsgwDir, predastoreDir, natsDir, spinifexDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory %s: %v\n", dir, err)
			os.Exit(1)
		}
	}

	portStr := fmt.Sprintf("%d", port)

	// Generate multi-node predastore config
	var predastoreNodeID int
	if len(predastoreNodes) >= 3 {
		predastoreContent, err := admin.GenerateMultiNodePredastoreConfig(predastoreMultiNodeTemplate, predastoreNodes, accessKey, secretKey, region, natsToken, configDir, bindIP)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating multi-node predastore config: %v\n", err)
			os.Exit(1)
		}

		predastorePath := filepath.Join(predastoreDir, "predastore.toml")
		if err := os.WriteFile(predastorePath, []byte(predastoreContent), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing predastore config: %v\n", err)
			os.Exit(1)
		}

		predastoreNodeID = admin.FindNodeIDByIP(predastoreNodes, bindIP)
		fmt.Printf("✅ Created: multi-node predastore.toml (node ID: %d)\n", predastoreNodeID)
	}

	spinifexTomlPath := filepath.Join(configDir, "spinifex.toml")

	configSettings := admin.ConfigSettings{
		AccessKey: accessKey,
		SecretKey: secretKey,
		AccountID: accountID,
		Region:    region,
		NatsToken: natsToken,
		DataDir:   spxRoot,
		ConfigDir: configDir,

		Node:          node,
		Az:            az,
		Port:          portStr,
		BindIP:        bindIP,
		ClusterBindIP: clusterBind,
		ClusterRoutes: clusterRoutes,
		ClusterName:   clusterName,

		PredastoreNodeID: predastoreNodeID,
		Services:         services,
		RemoteNodes:      buildRemoteNodes(allNodes, node),

		// Init node runs ovn-central locally
		OVNNBAddr: "tcp:127.0.0.1:6641",
		OVNSBAddr: "tcp:127.0.0.1:6642",
	}

	// Generate config files
	configs := []admin.ConfigFile{
		{Name: "spinifex.toml", Path: spinifexTomlPath, Template: spinifexTomlTemplate},
		{Name: filepath.Join(awsgwDir, "awsgw.toml"), Path: filepath.Join(awsgwDir, "awsgw.toml"), Template: awsgwTomlTemplate},
		{Name: filepath.Join(natsDir, "nats.conf"), Path: filepath.Join(natsDir, "nats.conf"), Template: natsConfTemplate},
	}
	// Skip template-based predastore.toml if multi-node was generated
	if len(predastoreNodes) < 3 {
		configs = append(configs, admin.ConfigFile{
			Name: filepath.Join(predastoreDir, "predastore.toml"), Path: filepath.Join(predastoreDir, "predastore.toml"), Template: predastoreTomlTemplate,
		})
	}

	if err := admin.GenerateConfigFiles(configs, configSettings); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating configuration files: %v\n", err)
		os.Exit(1)
	}

	// Update ~/.aws/credentials and ~/.aws/config with admin credentials (not system)
	fmt.Println("\n🔧 Configuring AWS credentials...")
	if err := admin.SetupAWSCredentials(bootstrapResult.AdminAccessKey, bootstrapResult.AdminSecretKey, region, certPath, bindIP); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not update AWS credentials: %v\n", err)
	} else {
		fmt.Println("✅ AWS credentials configured")
	}

	admin.CreateServiceDirectories(spxRoot)

	// Keep formation server running briefly so joining nodes can fetch complete status
	fmt.Println("\n⏳ Waiting for joining nodes to fetch cluster data...")
	time.Sleep(15 * time.Second)

	// Shutdown formation server
	fs.Shutdown(context.Background())

	// Print cluster summary
	fmt.Println("\n🎉 Cluster formation complete!")
	fmt.Printf("   Cluster: %s (%d nodes)\n", clusterName, expectedNodes)
	fmt.Printf("   Region: %s\n", region)
	fmt.Println("   Nodes:")
	for name, n := range allNodes {
		fmt.Printf("     - %s (%s)\n", name, n.BindIP)
	}
	fmt.Println("\n📋 Next steps:")
	fmt.Println("   1. Start services on ALL nodes:")
	fmt.Println("      ./scripts/start-dev.sh")
	fmt.Println()
}

func runAdminJoin(cmd *cobra.Command, args []string) {
	node, _ := cmd.Flags().GetString("node")
	leaderHost, _ := cmd.Flags().GetString("host")
	region, _ := cmd.Flags().GetString("region")
	az, _ := cmd.Flags().GetString("az")
	dataDir, _ := cmd.Flags().GetString("data-dir")
	port, _ := cmd.Flags().GetInt("port")
	bindIP, _ := cmd.Flags().GetString("bind")
	configDir, _ := cmd.Flags().GetString("config-dir")
	clusterBind, _ := cmd.Flags().GetString("cluster-bind")
	services, _ := cmd.Flags().GetStringSlice("services")

	// Validate required parameters
	if node == "" {
		fmt.Fprintf(os.Stderr, "❌ Error: --node is required\n")
		os.Exit(1)
	}
	if leaderHost == "" {
		fmt.Fprintf(os.Stderr, "❌ Error: --host is required\n")
		os.Exit(1)
	}

	// Extract leader IP for OVN NB/SB DB address (strip port from host:port)
	leaderIP, _, err := net.SplitHostPort(leaderHost)
	if err != nil {
		// leaderHost might be an IP without port
		leaderIP = leaderHost
	}

	// Validate IP address format
	if net.ParseIP(bindIP) == nil {
		fmt.Fprintf(os.Stderr, "❌ Error: Invalid IP address for --bind: %s\n", bindIP)
		os.Exit(1)
	}

	// Validate port range
	if port < 1 || port > 65535 {
		fmt.Fprintf(os.Stderr, "❌ Error: Port must be between 1 and 65535, got: %d\n", port)
		os.Exit(1)
	}

	// Default cluster-bind to bind IP if not specified
	if clusterBind == "" {
		clusterBind = bindIP
	}

	// Set default data directory
	if dataDir == "" {
		dataDir = DefaultDataDir()
	}

	fmt.Println("🚀 Joining Spinifex cluster...")
	fmt.Printf("Node: %s\n", node)
	fmt.Printf("Leader: %s\n", leaderHost)
	fmt.Printf("Region: %s\n", region)
	fmt.Printf("AZ: %s\n", az)
	fmt.Printf("Bind IP: %s\n", bindIP)
	fmt.Printf("Port: %d\n\n", port)

	// POST join request to formation server
	joinReq := formation.JoinRequest{
		NodeInfo: formation.NodeInfo{
			Name:      node,
			BindIP:    bindIP,
			ClusterIP: clusterBind,
			Region:    region,
			AZ:        az,
			Port:      port,
		},
	}

	reqBody, err := json.Marshal(joinReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling join request: %v\n", err)
		os.Exit(1)
	}

	client := &http.Client{Timeout: 30 * time.Second}

	joinURL := fmt.Sprintf("http://%s/formation/join", leaderHost)
	resp, err := client.Post(joinURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error connecting to formation server: %v\n", err)
		fmt.Fprintf(os.Stderr, "Make sure the leader node has run 'spx admin init' and is accessible at %s\n", leaderHost)
		os.Exit(1)
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error reading response body: %v\n", err)
		os.Exit(1)
	}

	var joinResp formation.JoinResponse
	if err := json.Unmarshal(body, &joinResp); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing join response: %v\n", err)
		os.Exit(1)
	}

	if !joinResp.Success {
		fmt.Fprintf(os.Stderr, "❌ Failed to join cluster: %s\n", joinResp.Message)
		os.Exit(1)
	}

	fmt.Printf("✅ Registered with formation server (%d/%d nodes joined)\n", joinResp.Joined, joinResp.Expected)

	// Poll status until formation is complete
	statusURL := fmt.Sprintf("http://%s/formation/status", leaderHost)
	var statusResp formation.StatusResponse

	for {
		sResp, err := client.Get(statusURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error polling formation status: %v\n", err)
			os.Exit(1)
		}

		sBody, err := io.ReadAll(sResp.Body)
		sResp.Body.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error reading status response: %v\n", err)
			os.Exit(1)
		}

		if err := json.Unmarshal(sBody, &statusResp); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing status response: %v\n", err)
			os.Exit(1)
		}

		if statusResp.Complete {
			break
		}

		fmt.Printf("   Waiting for cluster formation... (%d/%d nodes joined)\n", statusResp.Joined, statusResp.Expected)
		time.Sleep(2 * time.Second)
	}

	fmt.Printf("✅ Cluster formation complete! (%d nodes)\n\n", statusResp.Expected)

	// Extract credentials and CA from formation status
	creds := statusResp.Credentials
	if creds == nil {
		fmt.Fprintf(os.Stderr, "❌ Error: formation server did not return credentials\n")
		os.Exit(1)
	}

	// Set up config directory
	if configDir == "" {
		configDir = DefaultConfigDir()
	}

	if err := os.MkdirAll(configDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating config directory: %v\n", err)
		os.Exit(1)
	}

	// Write CA cert and key
	caCertPath := filepath.Join(configDir, "ca.pem")
	caKeyPath := filepath.Join(configDir, "ca.key")

	if statusResp.CACert == "" || statusResp.CAKey == "" {
		fmt.Fprintf(os.Stderr, "❌ Error: formation server did not return CA certificate\n")
		os.Exit(1)
	}

	if err := os.WriteFile(caCertPath, []byte(statusResp.CACert), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing CA cert: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(caKeyPath, []byte(statusResp.CAKey), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing CA key: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ CA certificate received from leader: %s\n", caCertPath)

	// Extract and write master key from formation server
	if statusResp.MasterKey == "" {
		fmt.Fprintf(os.Stderr, "❌ Error: formation server did not return master key\n")
		os.Exit(1)
	}
	masterKeyBytes, err := base64.StdEncoding.DecodeString(statusResp.MasterKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error decoding master key: %v\n", err)
		os.Exit(1)
	}
	if err := writeBootstrapFilesWithAdmin(configDir, masterKeyBytes, creds.AccessKey, creds.SecretKey, creds.AccountID, creds.AdminAccessKey, creds.AdminSecretKey); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error writing bootstrap files: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✅ IAM master key received from leader")
	fmt.Printf("✅ Bootstrap file written: %s\n", filepath.Join(configDir, "bootstrap.json"))

	// Generate server cert signed by CA with this node's bind IP
	if err := admin.GenerateServerCertOnly(configDir, bindIP); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating server certificate: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Server certificate generated with bind IP: %s\n\n", bindIP)

	// Build cluster topology from formation data
	clusterRoutes := formation.BuildClusterRoutes(statusResp.Nodes)
	predastoreNodes := formation.BuildPredastoreNodes(statusResp.Nodes)

	fmt.Println("📝 Creating configuration files...")

	// Create subdirectories
	awsgwDir := filepath.Join(configDir, "awsgw")
	predastoreDir := filepath.Join(configDir, "predastore")
	natsDir := filepath.Join(configDir, "nats")
	spinifexDir := filepath.Join(configDir, "spinifex")

	for _, dir := range []string{awsgwDir, predastoreDir, natsDir, spinifexDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory %s: %v\n", dir, err)
			os.Exit(1)
		}
	}

	portStr := fmt.Sprintf("%d", port)

	// Generate multi-node predastore config
	var predastoreNodeID int
	hasPredastoreConfig := len(predastoreNodes) >= 3

	if hasPredastoreConfig {
		predastoreContent, err := admin.GenerateMultiNodePredastoreConfig(predastoreMultiNodeTemplate, predastoreNodes, creds.AccessKey, creds.SecretKey, creds.Region, creds.NatsToken, configDir, bindIP)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating multi-node predastore config: %v\n", err)
			os.Exit(1)
		}

		predastorePath := filepath.Join(predastoreDir, "predastore.toml")
		if err := os.WriteFile(predastorePath, []byte(predastoreContent), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing predastore config: %v\n", err)
			os.Exit(1)
		}

		predastoreNodeID = admin.FindNodeIDByIP(predastoreNodes, bindIP)
		if predastoreNodeID == 0 {
			fmt.Fprintf(os.Stderr, "❌ Error: bind IP %s not found in predastore node list\n", bindIP)
			os.Exit(1)
		}
		fmt.Printf("✅ Created: multi-node predastore.toml (node ID: %d)\n", predastoreNodeID)
	}

	spinifexTomlPath := filepath.Join(configDir, "spinifex.toml")

	configSettings := admin.ConfigSettings{
		AccessKey: creds.AccessKey,
		SecretKey: creds.SecretKey,
		AccountID: creds.AccountID,
		Region:    creds.Region,
		NatsToken: creds.NatsToken,
		DataDir:   dataDir,
		ConfigDir: configDir,

		Node:          node,
		Az:            az,
		Port:          portStr,
		BindIP:        bindIP,
		ClusterBindIP: clusterBind,
		ClusterRoutes: clusterRoutes,
		ClusterName:   creds.ClusterName,

		PredastoreNodeID: predastoreNodeID,
		Services:         services,
		RemoteNodes:      buildRemoteNodes(statusResp.Nodes, node),

		// Joining nodes connect to the init node's OVN NB/SB DB
		OVNNBAddr: fmt.Sprintf("tcp:%s:6641", leaderIP),
		OVNSBAddr: fmt.Sprintf("tcp:%s:6642", leaderIP),
	}

	// Generate config files
	configs := []admin.ConfigFile{
		{Name: "spinifex.toml", Path: spinifexTomlPath, Template: spinifexTomlTemplate},
		{Name: filepath.Join(awsgwDir, "awsgw.toml"), Path: filepath.Join(awsgwDir, "awsgw.toml"), Template: awsgwTomlTemplate},
		{Name: filepath.Join(natsDir, "nats.conf"), Path: filepath.Join(natsDir, "nats.conf"), Template: natsConfTemplate},
	}
	// Skip template-based predastore.toml if multi-node was generated
	if !hasPredastoreConfig {
		configs = append(configs, admin.ConfigFile{
			Name: filepath.Join(predastoreDir, "predastore.toml"), Path: filepath.Join(predastoreDir, "predastore.toml"), Template: predastoreTomlTemplate,
		})
	}

	err = admin.GenerateConfigFiles(configs, configSettings)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating configuration files: %v\n", err)
		os.Exit(1)
	}

	// Update ~/.aws/credentials and ~/.aws/config with admin credentials (not system)
	fmt.Println("\n🔧 Configuring AWS credentials...")
	if err := admin.SetupAWSCredentials(creds.AdminAccessKey, creds.AdminSecretKey, creds.Region, caCertPath, bindIP); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not update AWS credentials: %v\n", err)
	} else {
		fmt.Println("✅ AWS credentials configured")
	}

	admin.CreateServiceDirectories(dataDir)

	// Print cluster summary
	fmt.Println("\n🎉 Node successfully joined cluster!")
	fmt.Printf("   Cluster: %s (%d nodes)\n", creds.ClusterName, len(statusResp.Nodes))
	fmt.Println("   Nodes:")
	for name, n := range statusResp.Nodes {
		fmt.Printf("     - %s (%s)\n", name, n.BindIP)
	}
	fmt.Println("\n📋 Next steps:")
	fmt.Println("   1. Start services:")
	fmt.Println("      ./scripts/start-dev.sh")
	fmt.Println()
}

// buildRemoteNodes converts formation NodeInfo into RemoteNode entries,
// excluding the local node. This puts all cluster members into spinifex.toml
// so config is the source of truth for expected cluster membership.
func buildRemoteNodes(allNodes map[string]formation.NodeInfo, localNode string) []admin.RemoteNode {
	var remote []admin.RemoteNode
	for name, n := range allNodes {
		if name == localNode {
			continue
		}
		remote = append(remote, admin.RemoteNode{
			Name:     name,
			Host:     n.BindIP,
			Region:   n.Region,
			AZ:       n.AZ,
			Services: n.Services,
		})
	}
	sort.Slice(remote, func(i, j int) bool {
		return remote[i].Name < remote[j].Name
	})
	return remote
}

// initIAMServiceFromConfig loads config, connects to NATS, loads the master
// key, and returns an initialised IAMServiceImpl. Callers must defer nc.Close().
func initIAMServiceFromConfig() (*handlers_iam.IAMServiceImpl, *config.ClusterConfig, *nats.Conn, func(), error) {
	cfg, nc, err := loadConfigAndConnect()
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("connect to cluster: %w", err)
	}

	masterKeyPath := filepath.Join(cfg.NodeBaseDir(), "config", "master.key")
	masterKey, err := handlers_iam.LoadMasterKey(masterKeyPath)
	if err != nil {
		nc.Close()
		return nil, nil, nil, nil, fmt.Errorf("load master key: %w", err)
	}

	svc, err := handlers_iam.NewIAMServiceImpl(nc, masterKey, len(cfg.Nodes))
	if err != nil {
		nc.Close()
		return nil, nil, nil, nil, fmt.Errorf("init IAM service: %w", err)
	}

	return svc, cfg, nc, func() { nc.Close() }, nil
}

// adminAccessPolicyDocument is the AdministratorAccess policy document that
// grants full access to all actions and resources.
const adminAccessPolicyDocument = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`

func runAccountCreate(cmd *cobra.Command, args []string) {
	name, _ := cmd.Flags().GetString("name")

	svc, cfg, nc, cleanup, err := initIAMServiceFromConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	// 1. Create the account
	account, err := svc.CreateAccount(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating account: %v\n", err)
		os.Exit(1)
	}
	accountID := account.AccountID

	// Create default VPC for the new account (belt-and-suspenders: daemon also
	// does this via iam.account.created event, but daemon may not be running).
	nodeConfig := cfg.Nodes[cfg.Node]
	vpcSvc, vpcErr := handlers_ec2_vpc.NewVPCServiceImplWithNATS(&nodeConfig, nc)
	if vpcErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create default VPC service: %v\n", vpcErr)
	} else if vpcErr = vpcSvc.EnsureDefaultVPC(accountID); vpcErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create default VPC: %v\n", vpcErr)
	}

	// 2. Create admin user
	_, err = svc.CreateUser(accountID, &iam.CreateUserInput{
		UserName: aws.String("admin"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating admin user: %v\n", err)
		os.Exit(1)
	}

	// 3. Create access key for admin user
	akOut, err := svc.CreateAccessKey(accountID, &iam.CreateAccessKeyInput{
		UserName: aws.String("admin"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating access key: %v\n", err)
		os.Exit(1)
	}

	// 4. Create AdministratorAccess policy scoped to this account
	policyARN := fmt.Sprintf("arn:aws:iam::%s:policy/AdministratorAccess", accountID)
	_, err = svc.CreatePolicy(accountID, &iam.CreatePolicyInput{
		PolicyName:     aws.String("AdministratorAccess"),
		PolicyDocument: aws.String(adminAccessPolicyDocument),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating admin policy: %v\n", err)
		os.Exit(1)
	}

	// 5. Attach policy to admin user
	_, err = svc.AttachUserPolicy(accountID, &iam.AttachUserPolicyInput{
		UserName:  aws.String("admin"),
		PolicyArn: aws.String(policyARN),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error attaching policy: %v\n", err)
		os.Exit(1)
	}

	// Configure AWS CLI profile automatically
	profileName := "spinifex-" + strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	homeDir, _ := os.UserHomeDir()

	endpointHost := "localhost"
	certPath := filepath.Join(cfg.NodeBaseDir(), "config", "ca.pem")
	nodeConfig = cfg.Nodes[cfg.Node]
	if h, _, err := net.SplitHostPort(nodeConfig.AWSGW.Host); err == nil {
		if h != "" && h != "0.0.0.0" {
			endpointHost = h
		}
	}
	endpointURL := fmt.Sprintf("https://%s:9999", endpointHost)

	credPath := filepath.Join(homeDir, ".aws", "credentials")
	configPath := filepath.Join(homeDir, ".aws", "config")

	if err := admin.UpdateAWSINIFile(credPath, profileName, map[string]string{
		"aws_access_key_id":     *akOut.AccessKey.AccessKeyId,
		"aws_secret_access_key": *akOut.AccessKey.SecretAccessKey,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update AWS credentials: %v\n", err)
	}
	region := cfg.Nodes[cfg.Node].Region
	if region == "" {
		region = "ap-southeast-2"
	}
	if err := admin.UpdateAWSINIFile(configPath, "profile "+profileName, map[string]string{
		"region":       region,
		"endpoint_url": endpointURL,
		"ca_bundle":    certPath,
		"output":       "json",
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not update AWS config: %v\n", err)
	}

	// Print credentials
	fmt.Println("\nAccount created successfully!")
	fmt.Printf("  Account ID:        %s\n", accountID)
	fmt.Printf("  Account Name:      %s\n", name)
	fmt.Printf("  Admin User:        admin\n")
	fmt.Printf("  Access Key ID:     %s\n", *akOut.AccessKey.AccessKeyId)
	fmt.Printf("  Secret Access Key: %s\n", *akOut.AccessKey.SecretAccessKey)
	fmt.Printf("  AWS Profile:       %s\n", profileName)
	fmt.Println("\nUse with:")
	fmt.Printf("  AWS_PROFILE=%s aws ec2 describe-instances\n", profileName)
}

func runAccountList(cmd *cobra.Command, args []string) {
	svc, _, _, cleanup, err := initIAMServiceFromConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	accounts, err := svc.ListAccounts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing accounts: %v\n", err)
		os.Exit(1)
	}

	if len(accounts) == 0 {
		fmt.Println("No accounts found.")
		return
	}

	fmt.Printf("%-14s %-20s %-10s %s\n", "ACCOUNT ID", "NAME", "STATUS", "CREATED")
	fmt.Printf("%-14s %-20s %-10s %s\n", "----------", "----", "------", "-------")
	for _, a := range accounts {
		created := a.CreatedAt
		if t, err := time.Parse(time.RFC3339, a.CreatedAt); err == nil {
			created = t.Format("2006-01-02 15:04")
		}
		fmt.Printf("%-14s %-20s %-10s %s\n", a.AccountID, a.AccountName, a.Status, created)
	}
}

// writeBootstrapResult holds the admin credentials so callers can
// write them to ~/.aws/credentials instead of the system credentials.
type writeBootstrapResult struct {
	AdminAccessKey string
	AdminSecretKey string
}

// writeBootstrapFiles generates new admin credentials and writes the bootstrap
// files (master.key + bootstrap.json). Used by init flows (single and multi-node).
func writeBootstrapFiles(configDir string, masterKey []byte, accessKey, secretKey, accountID string) (*writeBootstrapResult, error) {
	adminAccessKey, err := admin.GenerateAWSAccessKey()
	if err != nil {
		return nil, fmt.Errorf("generate admin access key: %w", err)
	}
	adminSecretKey, err := admin.GenerateAWSSecretKey()
	if err != nil {
		return nil, fmt.Errorf("generate admin secret key: %w", err)
	}
	if err := writeBootstrapFilesWithAdmin(configDir, masterKey, accessKey, secretKey, accountID, adminAccessKey, adminSecretKey); err != nil {
		return nil, err
	}
	return &writeBootstrapResult{
		AdminAccessKey: adminAccessKey,
		AdminSecretKey: adminSecretKey,
	}, nil
}

// writeBootstrapFilesWithAdmin writes the bootstrap files using the provided
// admin credentials. Used by join flows where admin creds come from the leader.
func writeBootstrapFilesWithAdmin(configDir string, masterKey []byte, accessKey, secretKey, accountID, adminAccessKey, adminSecretKey string) error {
	if err := handlers_iam.SaveMasterKey(filepath.Join(configDir, "master.key"), masterKey); err != nil {
		return fmt.Errorf("saving master key: %w", err)
	}
	encryptedSecret, err := handlers_iam.EncryptSecret(secretKey, masterKey)
	if err != nil {
		return fmt.Errorf("encrypting system secret: %w", err)
	}

	adminEncryptedSecret, err := handlers_iam.EncryptSecret(adminSecretKey, masterKey)
	if err != nil {
		return fmt.Errorf("encrypting admin secret: %w", err)
	}

	bd := &handlers_iam.BootstrapData{
		Version:         handlers_iam.BootstrapVersion,
		AccessKeyID:     accessKey,
		EncryptedSecret: encryptedSecret,
		AccountID:       accountID,
		Admin: &handlers_iam.AdminBootstrapData{
			AccountID:       admin.DefaultAccountID(),
			AccountName:     admin.DefaultAccountName(),
			UserName:        "admin",
			AccessKeyID:     adminAccessKey,
			EncryptedSecret: adminEncryptedSecret,
		},
	}

	return handlers_iam.SaveBootstrapData(filepath.Join(configDir, "bootstrap.json"), bd)
}
