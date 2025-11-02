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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"text/template"
	"time"

	"github.com/mulgadc/viperblock/viperblock"
	"github.com/spf13/cobra"
	"gopkg.in/ini.v1"
)

//go:embed templates/hive.toml
var hiveTomlTemplate string

//go:embed templates/awsgw.toml
var awsgwTomlTemplate string

//go:embed templates/predastore.toml
var predastoreTomlTemplate string

//go:embed templates/nats.conf
var natsConfTemplate string

var supportedArchs = map[string]bool{
	"x86_64":  true,
	"aarch64": true,
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

func init() {
	rootCmd.AddCommand(adminCmd)
	adminCmd.AddCommand(adminInitCmd)

	adminCmd.AddCommand(imagesCmd)
	imagesCmd.AddCommand(imagesImportCmd)

	homeDir, _ := os.UserHomeDir()
	configDir := fmt.Sprintf("%s/hive/config", homeDir)
	hiveDir := fmt.Sprintf("%s/hive/", homeDir)

	rootCmd.PersistentFlags().String("config-dir", configDir, "Configuration directory")
	rootCmd.PersistentFlags().String("hive-dir", hiveDir, "Hive base directory")

	// Flags for admin init
	adminInitCmd.Flags().Bool("force", false, "Force re-initialization (overwrites existing config)")
	adminInitCmd.Flags().String("region", "ap-southeast-2", "Mulga region to create")

	imagesImportCmd.Flags().String("file", "", "Import file from specified path (raw, qcow2, compressed)")
	imagesImportCmd.Flags().String("distro", "", "Specified distro name (e.g debian)")
	imagesImportCmd.Flags().String("version", "", "Specified distro version (e.g 12)")
	imagesImportCmd.Flags().String("arch", "", "Specified distro arch (e.g aarch64, arm64, x86_64)")
	imagesImportCmd.Flags().String("platform", "Linux/UNIX", "Specified platform (e.g Linux/UNIX, Windows)")

}

func runimagesImportCmd(cmd *cobra.Command, args []string) {

	imageFile, _ := cmd.Flags().GetString("file")

	if imageFile == "" {
		fmt.Fprintf(os.Stderr, "File required to import image")
		os.Exit(1)
	}

	imageStat, err := os.Stat(imageFile)

	if err != nil {
		fmt.Fprintf(os.Stderr, "File could not be found", err)
		os.Exit(1)
	}

	//configDir, _ := cmd.Flags().GetString("config-dir")
	baseDir, _ := cmd.Flags().GetString("hive-dir")

	// Check the base dir has our images path, and correctlty init
	imageDir := fmt.Sprintf("%s/images", baseDir)

	if !fileExists(imageDir) {
		fmt.Fprintf(os.Stderr, "Image directory does not exist. Base path specified correctly?", imageDir)
		os.Exit(1)
	}

	distro, _ := cmd.Flags().GetString("distro")
	version, _ := cmd.Flags().GetString("version")
	arch, _ := cmd.Flags().GetString("arch")
	platform, _ := cmd.Flags().GetString("platform")

	if distro == "" {
		fmt.Fprintf(os.Stderr, "Specify distro name")
		os.Exit(1)
	}

	// Check version specified
	if version == "" {
		fmt.Fprintf(os.Stderr, "Specify image version")
		os.Exit(1)
	}

	if !supportedArchs[arch] {
		fmt.Fprintf(os.Stderr, "Unsupported architecture")
		os.Exit(1)
	}

	if !supportedPlatforms[platform] {
		fmt.Fprintf(os.Stderr, "Unsupported platform")
		os.Exit(1)
	}

	// Create the specified image directory
	imagePath := fmt.Sprintf("%s/%s/%s/%s", imageDir, distro, version, arch)

	// Create config directory
	if err := os.MkdirAll(imagePath, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating config directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Created config directory: %s\n", imagePath)

	// Create the specified manifest file to describe the image/AMI
	manifest := viperblock.VolumeConfig{}

	// Calculate the size
	manifest.AMIMetadata.Name = fmt.Sprintf("%s-%s-%s-ami", distro, version, arch)

	manifest.AMIMetadata.Description = fmt.Sprintf("%s cloud image prepared for Hive", manifest.AMIMetadata.Name)
	manifest.AMIMetadata.Architecture = arch
	manifest.AMIMetadata.PlatformDetails = platform
	manifest.AMIMetadata.CreationDate = time.Now()
	manifest.AMIMetadata.RootDeviceType = "ebs"
	manifest.AMIMetadata.Virtualization = "hvm"
	manifest.AMIMetadata.ImageOwnerAlias = "system"
	manifest.AMIMetadata.VolumeSizeGiB = uint64(imageStat.Size() / 1024 / 1024 / 1024)

	// Volume Data
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
	err = os.WriteFile(manifestFilename, jsonData, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not write manifest: %v\n", err)
		os.Exit(1)
	}

}

func runAdminInit(cmd *cobra.Command, args []string) {
	force, _ := cmd.Flags().GetBool("force")
	configDir, _ := cmd.Flags().GetString("config-dir")
	region, _ := cmd.Flags().GetString("region")

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
	if !force && fileExists(hiveTomlPath) {
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
	accessKey := generateAWSAccessKey()
	secretKey := generateAWSSecretKey()
	accountID := generateAccountID()

	fmt.Println("\n🔑 Generated AWS credentials:")
	fmt.Printf("   Access Key: %s\n", accessKey)
	fmt.Printf("   Secret Key: %s\n", secretKey)
	fmt.Printf("   Account ID: %s\n", accountID)

	// Generate SSL certificates
	certPath := filepath.Join(configDir, "server.pem")
	keyPath := filepath.Join(configDir, "server.key")

	if force || !fileExists(certPath) || !fileExists(keyPath) {
		fmt.Println("\n🔐 Generating SSL certificates...")
		if err := generateSelfSignedCert(certPath, keyPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error generating SSL certificates: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ SSL certificates generated:\n")
		fmt.Printf("   Certificate: %s\n", certPath)
		fmt.Printf("   Key: %s\n", keyPath)
	} else {
		fmt.Println("\n✅ SSL certificates already exist")
	}

	// Generate NATS token
	natsToken := generateNATSToken()
	fmt.Println("\n🔒 Generated NATS authentication token")

	// Get home directory for data path
	homeDir, _ := os.UserHomeDir()
	hiveRoot := filepath.Join(homeDir, "hive")

	// Create config files from embedded templates
	fmt.Println("\n📝 Creating configuration files...")

	// Create subdirectories
	awsgwDir := filepath.Join(configDir, "awsgw")
	predastoreDir := filepath.Join(configDir, "predastore")
	natsDir := filepath.Join(configDir, "nats")

	for _, dir := range []string{awsgwDir, predastoreDir, natsDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory %s: %v\n", dir, err)
			os.Exit(1)
		}
	}

	// Generate all config files
	configs := []struct {
		path     string
		template string
		name     string
	}{
		{hiveTomlPath, hiveTomlTemplate, "hive.toml"},
		{filepath.Join(awsgwDir, "awsgw.toml"), awsgwTomlTemplate, "awsgw/awsgw.toml"},
		{filepath.Join(predastoreDir, "predastore.toml"), predastoreTomlTemplate, "predastore/predastore.toml"},
		{filepath.Join(natsDir, "nats.conf"), natsConfTemplate, "nats/nats.conf"},
	}

	for _, cfg := range configs {
		if err := generateConfigFile(cfg.path, cfg.template, accessKey, secretKey, accountID, region, natsToken, hiveRoot); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", cfg.name, err)
			os.Exit(1)
		}
		fmt.Printf("✅ Created: %s\n", cfg.name)
	}

	// Update ~/.aws/credentials and ~/.aws/config
	fmt.Println("\n🔧 Configuring AWS credentials...")
	if err := setupAWSCredentials(accessKey, secretKey, region, certPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not update AWS credentials: %v\n", err)
	} else {
		fmt.Println("✅ AWS credentials configured")
	}

	// Create additional directories
	dirs := []string{
		filepath.Join(hiveRoot, "images"),
		filepath.Join(hiveRoot, "amis"),
		filepath.Join(hiveRoot, "volumes"),
		filepath.Join(hiveRoot, "state"),
		filepath.Join(hiveRoot, "logs"),
		filepath.Join(hiveRoot, "nats"),
		filepath.Join(hiveRoot, "predastore"),
		filepath.Join(hiveRoot, "viperblock"),
	}

	fmt.Println("\n📁 Creating directory structure...")
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not create %s: %v\n", dir, err)
		}
	}
	fmt.Printf("✅ Directory structure created in %s\n", hiveRoot)

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

// generateAWSAccessKey generates an AWS-style access key
// Format: AKIA + 16 random uppercase alphanumeric characters
func generateAWSAccessKey() string {
	const prefix = "AKIA"
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 16

	result := make([]byte, length)
	for i := range result {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		result[i] = charset[num.Int64()]
	}

	return prefix + string(result)
}

// generateAWSSecretKey generates an AWS-style secret key
// 40 character base64-encoded string
func generateAWSSecretKey() string {
	bytes := make([]byte, 30) // 30 bytes = 40 chars in base64
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(bytes)
}

// generateAccountID generates a 12-digit AWS account ID
func generateAccountID() string {
	// Generate random 12-digit number
	num, _ := rand.Int(rand.Reader, big.NewInt(900000000000))
	accountID := num.Int64() + 100000000000 // Ensure it's 12 digits
	return fmt.Sprintf("%012d", accountID)
}

// generateNATSToken generates a secure random token for NATS
func generateNATSToken() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return "nats_" + base64.URLEncoding.EncodeToString(bytes)[:32]
}

// generateSelfSignedCert generates a self-signed SSL certificate
func generateSelfSignedCert(certPath, keyPath string) error {
	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate template
	notBefore := time.Now()
	notAfter := notBefore.Add(3650 * 24 * time.Hour) // 10 years

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "localhost",
			Organization: []string{"Hive Platform"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	// Create certificate
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	// Write certificate to file
	certOut, err := os.Create(certPath)
	if err != nil {
		return fmt.Errorf("failed to create cert file: %w", err)
	}
	defer certOut.Close()

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return fmt.Errorf("failed to write cert: %w", err)
	}

	// Write private key to file
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create key file: %w", err)
	}
	defer keyOut.Close()

	privBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}

	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		return fmt.Errorf("failed to write key: %w", err)
	}

	return nil
}

// generateConfigFile creates a configuration file from a template
func generateConfigFile(path, templateContent, accessKey, secretKey, accountID, region, natsToken, dataDir string) error {
	// Parse the embedded template
	tmpl, err := template.New("config").Parse(templateContent)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	// Template data
	data := struct {
		AccessKey string
		SecretKey string
		AccountID string
		Region    string
		NatsToken string
		DataDir   string
	}{
		AccessKey: accessKey,
		SecretKey: secretKey,
		AccountID: accountID,
		Region:    region,
		NatsToken: natsToken,
		DataDir:   dataDir,
	}

	// Create file with secure permissions
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer f.Close()

	// Execute template
	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return nil
}

// setupAWSCredentials updates ~/.aws/credentials and ~/.aws/config
func setupAWSCredentials(accessKey, secretKey, region, certPath string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	awsDir := filepath.Join(homeDir, ".aws")
	if err := os.MkdirAll(awsDir, 0700); err != nil {
		return err
	}

	credPath := filepath.Join(awsDir, "credentials")
	configPath := filepath.Join(awsDir, "config")

	// Determine profile name
	profileName := "default"
	if fileExists(credPath) {
		// Check if default profile already exists
		cfg, err := ini.Load(credPath)
		if err == nil && cfg.HasSection("default") {
			profileName = "hive"
		}
	}

	// Update credentials file
	if err := updateAWSINIFile(credPath, profileName, map[string]string{
		"aws_access_key_id":     accessKey,
		"aws_secret_access_key": secretKey,
	}); err != nil {
		return err
	}

	// Update config file
	configSection := profileName
	if profileName != "default" {
		configSection = "profile " + profileName
	}

	if err := updateAWSINIFile(configPath, configSection, map[string]string{
		"region":       region,
		"endpoint_url": "https://localhost:9999",
		"ca_bundle":    certPath,
		"output":       "json",
	}); err != nil {
		return err
	}

	fmt.Printf("   Profile: %s\n", profileName)
	if profileName != "default" {
		fmt.Printf("   Use: export AWS_PROFILE=%s\n", profileName)
	}

	return nil
}

// Helper functions

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// updateAWSINIFile updates or creates an AWS INI file section with given key-value pairs
func updateAWSINIFile(path, section string, values map[string]string) error {
	var cfg *ini.File
	var err error

	// Load existing file or create new one
	if fileExists(path) {
		cfg, err = ini.Load(path)
		if err != nil {
			return fmt.Errorf("failed to load INI file: %w", err)
		}
	} else {
		cfg = ini.Empty()
	}

	// Get or create section
	sec, err := cfg.NewSection(section)
	if err != nil {
		// Section already exists, get it
		sec, err = cfg.GetSection(section)
		if err != nil {
			return fmt.Errorf("failed to get section: %w", err)
		}
	}

	// Set key-value pairs
	for key, value := range values {
		sec.Key(key).SetValue(value)
	}

	// Save with proper permissions
	return cfg.SaveTo(path)
}
