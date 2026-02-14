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

	"github.com/mulgadc/hive/hive/admin"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/formation"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/viperblock/viperblock"
	"github.com/mulgadc/viperblock/viperblock/backends/s3"
	"github.com/mulgadc/viperblock/viperblock/v_utils"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
)

//go:embed templates/hive.toml
var hiveTomlTemplate string

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
	Short: "Administrative commands for Hive platform management",
	Long:  `Administrative commands for initializing and managing the Hive platform.`,
}

var adminInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Hive platform configuration",
	Long: `Initialize Hive platform by creating configuration files, generating SSL certificates,
and setting up AWS credentials. This creates the necessary directory structure and
configuration files in ~/hive/config.`,
	Run: runAdminInit,
}

var adminJoinCmd = &cobra.Command{
	Use:   "join",
	Short: "Join an existing Hive cluster",
	Long: `Join an existing Hive cluster by connecting to a leader node and retrieving
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

/*
CLI ideas

hive admin images list

- fetches from remote endpoint for common/trusted images to bootstrap environment, or baked in from compile.

// If --name specified, download
hive admin images import --name debian-12-x86_64

// List available images
hive admin images list

// Manually import a path
hive admin images import --file /path/to/image --distro debian --version 12 --arch x86_64

-> x <-
*/

func init() {
	rootCmd.AddCommand(adminCmd)
	adminCmd.AddCommand(adminInitCmd)
	adminCmd.AddCommand(adminJoinCmd)

	adminCmd.AddCommand(imagesCmd)
	imagesCmd.AddCommand(imagesImportCmd)
	imagesCmd.AddCommand(imagesListCmd)

	homeDir, _ := os.UserHomeDir()
	configDir := fmt.Sprintf("%s/hive/config", homeDir)
	hiveDir := fmt.Sprintf("%s/hive/", homeDir)

	rootCmd.PersistentFlags().String("config-dir", configDir, "Configuration directory")
	rootCmd.PersistentFlags().String("hive-dir", hiveDir, "Hive base directory")

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
	adminInitCmd.Flags().String("cluster-name", "hive", "NATS cluster name")
	adminInitCmd.Flags().StringSlice("services", nil, "Services this node runs (default: all). Valid: nats,predastore,viperblock,daemon,awsgw,ui")

	// Flags for admin join
	adminJoinCmd.Flags().String("region", "ap-southeast-2", "Region for this node")
	adminJoinCmd.Flags().String("az", "ap-southeast-2a", "Availability zone for this node")
	adminJoinCmd.Flags().String("node", "", "Node name (required)")
	adminJoinCmd.Flags().String("host", "", "Leader node host:port (e.g., node1.local:4432) (required)")
	adminJoinCmd.Flags().String("data-dir", "", "Data directory for this node (default: ~/hive)")
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
		homeDir, _ := os.UserHomeDir()
		cfgFile = fmt.Sprintf("%s/hive/config/hive.toml", homeDir)
	}

	//configDir, _ := cmd.Flags().GetString("config-dir")
	baseDir, _ := cmd.Flags().GetString("hive-dir")

	// Strip trailing slash
	baseDir = filepath.Clean(baseDir)

	// Check the base dir has our images path, and correctlty init
	imageDir := fmt.Sprintf("%s/images", baseDir)

	if !admin.FileExists(imageDir) {
		fmt.Fprintf(os.Stderr, "Image directory does not exist. Base path specified correctly? %s", imageDir)
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

		//imageStat, err = os.Stat(imageFile)

		if err != nil {
			fmt.Fprintf(os.Stderr, "File could not be found %s", err)
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
	tmpDir, err := os.MkdirTemp(ostmpDir, "hive-image-tmp-*")

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

	manifest.AMIMetadata.Description = fmt.Sprintf("%s cloud image prepared for Hive", manifest.AMIMetadata.Name)
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
		AccessKey:  appConfig.Nodes[appConfig.Node].AccessKey,
		SecretKey:  appConfig.Nodes[appConfig.Node].SecretKey,
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

	pterm.Println("hive admin images import --name <image-name>")
}

// TODO: Move all logic to a module, use minimal application logic in viper commands
func runAdminInit(cmd *cobra.Command, args []string) {
	force, _ := cmd.Flags().GetBool("force")
	configDir, _ := cmd.Flags().GetString("config-dir")
	hiveRoot, _ := cmd.Flags().GetString("hive-dir")
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

	fmt.Printf("Initializing Hive with bind IP: %s, port: %d\n", bindIP, port)

	// Default config directory
	if configDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting home directory: %v\n", err)
			os.Exit(1)
		}
		configDir = filepath.Join(homeDir, "hive", "config")
	}

	fmt.Println("🚀 Initializing Hive platform...")
	fmt.Printf("Configuration directory: %s\n\n", configDir)

	// Check if already initialized
	hiveTomlPath := filepath.Join(configDir, "hive.toml")
	if !force && admin.FileExists(hiveTomlPath) {
		fmt.Println("⚠️  Hive already initialized!")
		fmt.Printf("Config file exists: %s\n", hiveTomlPath)
		fmt.Println("\nTo re-initialize, run with --force flag:")
		fmt.Println("  hive admin init --force")
		os.Exit(0)
	}

	// Create config directory
	if err := os.MkdirAll(configDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating config directory: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ Created config directory: %s\n", configDir)

	// Generate AWS credentials
	accessKey := admin.GenerateAWSAccessKey()
	secretKey := admin.GenerateAWSSecretKey()
	accountID := admin.GenerateAccountID()

	fmt.Println("\n🔑 Generated AWS credentials:")
	fmt.Printf("   Access Key: %s\n", accessKey)
	fmt.Printf("   Secret Key: %s\n", secretKey)
	fmt.Printf("   Account ID: %s\n", accountID)

	// Generate SSL certificates (with bind IP in SANs for multi-node support)
	certPath := admin.GenerateCertificatesIfNeeded(configDir, force, bindIP)

	// Generate NATS token
	natsToken := admin.GenerateNATSToken()
	fmt.Println("\n🔒 Generated NATS authentication token")

	if hiveRoot == "" {
		// Get home directory for data path
		homeDir, _ := os.UserHomeDir()
		hiveRoot = filepath.Join(homeDir, "hive")
	}

	// Determine if this is a multi-node formation
	isMultiNode := nodes >= 2 && bindIP != "0.0.0.0"

	if isMultiNode {
		runAdminInitMultiNode(cmd, accessKey, secretKey, accountID, natsToken, clusterName,
			configDir, hiveRoot, certPath, region, az, node, bindIP, clusterBind,
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
	hiveDir := filepath.Join(configDir, "hive")

	for _, dir := range []string{awsgwDir, predastoreDir, natsDir, hiveDir} {
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
		predastoreContent, err := admin.GenerateMultiNodePredastoreConfig(predastoreMultiNodeTemplate, predastoreNodes, accessKey, secretKey, region)
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
		DataDir:   hiveRoot,

		Node:          node,
		Az:            az,
		Port:          portStr,
		BindIP:        bindIP,
		ClusterBindIP: clusterBind,
		ClusterRoutes: clusterRoutes,
		ClusterName:   clusterName,

		PredastoreNodeID: predastoreNodeID,
		Services:         services,
	}

	// Generate config files
	configs := []admin.ConfigFile{
		{Name: "hive.toml", Path: hiveTomlPath, Template: hiveTomlTemplate},
		{Name: filepath.Join(awsgwDir, "awsgw.toml"), Path: filepath.Join(awsgwDir, "awsgw.toml"), Template: awsgwTomlTemplate},
		{Name: filepath.Join(natsDir, "nats.conf"), Path: filepath.Join(natsDir, "nats.conf"), Template: natsConfTemplate},
	}
	// Skip template-based predastore.toml if multi-node was already generated
	if predastoreNodesStr == "" {
		configs = append(configs, admin.ConfigFile{
			Name: filepath.Join(predastoreDir, "predastore.toml"), Path: filepath.Join(predastoreDir, "predastore.toml"), Template: predastoreTomlTemplate,
		})
	}

	err := admin.GenerateConfigFiles(configs, configSettings)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating configuration files: %v\n", err)
		os.Exit(1)
	}

	// Update ~/.aws/credentials and ~/.aws/config
	fmt.Println("\n🔧 Configuring AWS credentials...")
	if err := admin.SetupAWSCredentials(accessKey, secretKey, region, certPath, bindIP); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not update AWS credentials: %v\n", err)
	} else {
		fmt.Println("✅ AWS credentials configured")
	}

	admin.CreateServiceDirectories(hiveRoot)

	// Print success message
	fmt.Println("\n🎉 Hive initialization complete!")
	fmt.Println("\n📋 Next steps:")
	fmt.Println("   1. Start services:")
	fmt.Println("      ./scripts/start-dev.sh")
	fmt.Println()
	fmt.Println("   2. Test with AWS CLI:")
	fmt.Println("      export AWS_PROFILE=hive")
	fmt.Println("      aws ec2 describe-instances")
	fmt.Println()
	fmt.Println("🔗 Configuration:")
	fmt.Printf("   Config file: %s\n", hiveTomlPath)
	fmt.Printf("   Data directory: %s\n", hiveRoot)
	fmt.Println()
}

// runAdminInitMultiNode handles the multi-node formation path for admin init.
// It starts a formation server, registers this node, waits for all nodes to join,
// then generates configs with complete cluster topology.
func runAdminInitMultiNode(cmd *cobra.Command, accessKey, secretKey, accountID, natsToken, clusterName,
	configDir, hiveRoot, certPath, region, az, node, bindIP, clusterBind string,
	port, expectedNodes int, formationTimeoutStr string, services []string) {

	formationTimeout, err := time.ParseDuration(formationTimeoutStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Error: Invalid --formation-timeout: %v\n", err)
		os.Exit(1)
	}

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
		AccessKey:   accessKey,
		SecretKey:   secretKey,
		AccountID:   accountID,
		NatsToken:   natsToken,
		ClusterName: clusterName,
		Region:      region,
	}

	fs := formation.NewFormationServer(expectedNodes, creds, string(caCertData), string(caKeyData))

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
	fmt.Printf("   Other nodes should run: hive admin join --host %s --node <name> --bind <ip>\n\n", formationAddr)

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
	hiveDir := filepath.Join(configDir, "hive")

	for _, dir := range []string{awsgwDir, predastoreDir, natsDir, hiveDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory %s: %v\n", dir, err)
			os.Exit(1)
		}
	}

	portStr := fmt.Sprintf("%d", port)

	// Generate multi-node predastore config
	var predastoreNodeID int
	if len(predastoreNodes) >= 3 {
		predastoreContent, err := admin.GenerateMultiNodePredastoreConfig(predastoreMultiNodeTemplate, predastoreNodes, accessKey, secretKey, region)
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

	hiveTomlPath := filepath.Join(configDir, "hive.toml")

	configSettings := admin.ConfigSettings{
		AccessKey: accessKey,
		SecretKey: secretKey,
		AccountID: accountID,
		Region:    region,
		NatsToken: natsToken,
		DataDir:   hiveRoot,

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
	}

	// Generate config files
	configs := []admin.ConfigFile{
		{Name: "hive.toml", Path: hiveTomlPath, Template: hiveTomlTemplate},
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

	// Update ~/.aws/credentials and ~/.aws/config
	fmt.Println("\n🔧 Configuring AWS credentials...")
	if err := admin.SetupAWSCredentials(accessKey, secretKey, region, certPath, bindIP); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not update AWS credentials: %v\n", err)
	} else {
		fmt.Println("✅ AWS credentials configured")
	}

	admin.CreateServiceDirectories(hiveRoot)

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
		homeDir, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Error getting home directory: %v\n", err)
			os.Exit(1)
		}
		dataDir = filepath.Join(homeDir, "hive")
	}

	fmt.Println("🚀 Joining Hive cluster...")
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
		fmt.Fprintf(os.Stderr, "Make sure the leader node has run 'hive admin init' and is accessible at %s\n", leaderHost)
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
		homeDir, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error getting home directory: %v\n", err)
			os.Exit(1)
		}
		configDir = filepath.Join(homeDir, "hive", "config")
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
	hiveDir := filepath.Join(configDir, "hive")

	for _, dir := range []string{awsgwDir, predastoreDir, natsDir, hiveDir} {
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
		predastoreContent, err := admin.GenerateMultiNodePredastoreConfig(predastoreMultiNodeTemplate, predastoreNodes, creds.AccessKey, creds.SecretKey, creds.Region)
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

	hiveTomlPath := filepath.Join(configDir, "hive.toml")

	configSettings := admin.ConfigSettings{
		AccessKey: creds.AccessKey,
		SecretKey: creds.SecretKey,
		AccountID: creds.AccountID,
		Region:    creds.Region,
		NatsToken: creds.NatsToken,
		DataDir:   dataDir,

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
	}

	// Generate config files
	configs := []admin.ConfigFile{
		{Name: "hive.toml", Path: hiveTomlPath, Template: hiveTomlTemplate},
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

	// Update ~/.aws/credentials and ~/.aws/config
	fmt.Println("\n🔧 Configuring AWS credentials...")
	if err := admin.SetupAWSCredentials(creds.AccessKey, creds.SecretKey, creds.Region, caCertPath, bindIP); err != nil {
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
// excluding the local node. This puts all cluster members into hive.toml
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
