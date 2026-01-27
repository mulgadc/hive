package admin

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"text/template"
	"time"

	"gopkg.in/ini.v1"
)

type ConfigSettings struct {
	AccessKey string
	SecretKey string
	AccountID string
	Region    string
	NatsToken string
	DataDir   string

	// Add more fields as needed
	Node   string
	Az     string
	Port   string
	BindIP string

	// Cluster settings
	ClusterBindIP string
	ClusterRoutes []string
}

type ConfigFile struct {
	Name     string
	Path     string
	Template string
}

func GenerateConfigFiles(configs []ConfigFile, configSettings ConfigSettings) error {

	for _, cfg := range configs {
		if err := GenerateConfigFile(cfg.Path, cfg.Template, configSettings); err != nil {
			return fmt.Errorf("error creating %s: %v", cfg.Name, err)
		}
		fmt.Printf("✅ Created: %s\n", cfg.Name)
	}

	return nil
}

// generateConfigFile creates a configuration file from a template
func GenerateConfigFile(configPath string, configTemplate string, configSettings ConfigSettings) error {
	// Parse the embedded template
	tmpl, err := template.New("config").Parse(configTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	// Create file with secure permissions
	f, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer f.Close()

	// Execute template
	if err := tmpl.Execute(f, configSettings); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return nil
}

func GenerateCertificatesIfNeeded(configDir string, force bool) (caCertPath string) {
	// Certificate paths
	caCertPath = filepath.Join(configDir, "ca.pem")
	caKeyPath := filepath.Join(configDir, "ca.key")
	serverCertPath := filepath.Join(configDir, "server.pem")
	serverKeyPath := filepath.Join(configDir, "server.key")

	// Check if we need to generate certificates
	needsGeneration := force ||
		!FileExists(caCertPath) || !FileExists(caKeyPath) ||
		!FileExists(serverCertPath) || !FileExists(serverKeyPath)

	if needsGeneration {
		fmt.Println("\n🔐 Generating Certificate Authority and SSL certificates...")

		// Step 1: Generate CA certificate
		if err := GenerateCACert(caCertPath, caKeyPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error generating CA certificate: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ CA certificate generated:\n")
		fmt.Printf("   CA Certificate: %s\n", caCertPath)
		fmt.Printf("   CA Key: %s\n", caKeyPath)

		// Step 2: Generate server certificate signed by CA
		if err := GenerateSignedCert(serverCertPath, serverKeyPath, caCertPath, caKeyPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error generating server certificate: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Server certificate generated (signed by CA):\n")
		fmt.Printf("   Certificate: %s\n", serverCertPath)
		fmt.Printf("   Key: %s\n", serverKeyPath)

		// Print instructions for adding CA to system trust store
		fmt.Println("\n📋 To trust the Hive CA system-wide (recommended):")
		fmt.Printf("   sudo cp %s /usr/local/share/ca-certificates/hive-ca.crt\n", caCertPath)
		fmt.Println("   sudo update-ca-certificates")
		fmt.Println("\n   This allows AWS CLI and other tools to trust Hive services automatically.")
	} else {
		fmt.Println("\n✅ CA and SSL certificates already exist")
	}

	return caCertPath
}

func CreateServiceDirectories(hiveRoot string) {

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
		filepath.Join(hiveRoot, "hive"),
	}

	fmt.Println("\n📁 Creating directory structure...")
	for _, dir := range dirs {

		// Check if directory exists
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			if err := os.MkdirAll(dir, 0755); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: Could not create %s: %v\n", dir, err)
			}
		}
	}
	fmt.Printf("✅ Directory structure created in %s\n", hiveRoot)

}

// Helper functions

func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// updateAWSINIFile updates or creates an AWS INI file section with given key-value pairs
func UpdateAWSINIFile(path, section string, values map[string]string) error {
	var cfg *ini.File
	var err error

	// Load existing file or create new one
	if FileExists(path) {
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

// generateAWSAccessKey generates an AWS-style access key
// Format: AKIA + 16 random uppercase alphanumeric characters
func GenerateAWSAccessKey() string {
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
func GenerateAWSSecretKey() string {
	bytes := make([]byte, 30) // 30 bytes = 40 chars in base64
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(bytes)
}

// generateAccountID generates a 12-digit AWS account ID
func GenerateAccountID() string {
	// Generate random 12-digit number
	num, _ := rand.Int(rand.Reader, big.NewInt(900000000000))
	accountID := num.Int64() + 100000000000 // Ensure it's 12 digits
	return fmt.Sprintf("%012d", accountID)
}

// generateNATSToken generates a secure random token for NATS
func GenerateNATSToken() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return "nats_" + base64.URLEncoding.EncodeToString(bytes)[:32]
}

// GenerateCACert generates a Certificate Authority certificate and key
func GenerateCACert(caCertPath, caKeyPath string) error {
	// Generate CA private key
	caPrivateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return fmt.Errorf("failed to generate CA private key: %w", err)
	}

	// Create CA certificate template
	notBefore := time.Now()
	notAfter := notBefore.Add(3650 * 24 * time.Hour) // 10 years

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %w", err)
	}

	caTemplate := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "Hive Local CA",
			Organization: []string{"Hive Platform"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}

	// Self-sign the CA certificate
	caDerBytes, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caPrivateKey.PublicKey, caPrivateKey)
	if err != nil {
		return fmt.Errorf("failed to create CA certificate: %w", err)
	}

	// Write CA certificate to file
	caCertOut, err := os.Create(caCertPath)
	if err != nil {
		return fmt.Errorf("failed to create CA cert file: %w", err)
	}
	defer caCertOut.Close()

	if err := pem.Encode(caCertOut, &pem.Block{Type: "CERTIFICATE", Bytes: caDerBytes}); err != nil {
		return fmt.Errorf("failed to write CA cert: %w", err)
	}

	// Write CA private key to file
	caKeyOut, err := os.OpenFile(caKeyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create CA key file: %w", err)
	}
	defer caKeyOut.Close()

	caPrivBytes, err := x509.MarshalPKCS8PrivateKey(caPrivateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal CA private key: %w", err)
	}

	if err := pem.Encode(caKeyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: caPrivBytes}); err != nil {
		return fmt.Errorf("failed to write CA key: %w", err)
	}

	return nil
}

// GenerateSignedCert generates a server certificate signed by the CA
func GenerateSignedCert(certPath, keyPath, caCertPath, caKeyPath string) error {
	// Load CA certificate
	caCertPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return fmt.Errorf("failed to read CA cert: %w", err)
	}

	caCertBlock, _ := pem.Decode(caCertPEM)
	if caCertBlock == nil {
		return fmt.Errorf("failed to decode CA cert PEM")
	}

	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse CA cert: %w", err)
	}

	// Load CA private key
	caKeyPEM, err := os.ReadFile(caKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read CA key: %w", err)
	}

	caKeyBlock, _ := pem.Decode(caKeyPEM)
	if caKeyBlock == nil {
		return fmt.Errorf("failed to decode CA key PEM")
	}

	caPrivateKey, err := x509.ParsePKCS8PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse CA private key: %w", err)
	}

	caRSAKey, ok := caPrivateKey.(*rsa.PrivateKey)
	if !ok {
		return fmt.Errorf("CA key is not RSA")
	}

	// Generate server private key
	serverPrivateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return fmt.Errorf("failed to generate server private key: %w", err)
	}

	// Create server certificate template
	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour) // 1 year for server certs

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %w", err)
	}

	serverTemplate := x509.Certificate{
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

	// Sign the server certificate with the CA
	serverDerBytes, err := x509.CreateCertificate(rand.Reader, &serverTemplate, caCert, &serverPrivateKey.PublicKey, caRSAKey)
	if err != nil {
		return fmt.Errorf("failed to create server certificate: %w", err)
	}

	// Write server certificate to file
	certOut, err := os.Create(certPath)
	if err != nil {
		return fmt.Errorf("failed to create cert file: %w", err)
	}
	defer certOut.Close()

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: serverDerBytes}); err != nil {
		return fmt.Errorf("failed to write cert: %w", err)
	}

	// Write server private key to file
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create key file: %w", err)
	}
	defer keyOut.Close()

	privBytes, err := x509.MarshalPKCS8PrivateKey(serverPrivateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}

	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		return fmt.Errorf("failed to write key: %w", err)
	}

	return nil
}

// generateSelfSignedCert generates a self-signed SSL certificate (legacy, kept for compatibility)
func GenerateSelfSignedCert(certPath, keyPath string) error {
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

// setupAWSCredentials updates ~/.aws/credentials and ~/.aws/config
func SetupAWSCredentials(accessKey, secretKey, region, certPath string) error {
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
	//profileName := "default"

	// Use hive as the default profile
	profileName := "hive"

	if FileExists(credPath) {
		// Check if default profile already exists
		cfg, err := ini.Load(credPath)
		if err == nil && cfg.HasSection("default") {
			profileName = "hive"
		}
	}

	// Update credentials file
	if err := UpdateAWSINIFile(credPath, profileName, map[string]string{
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

	if err := UpdateAWSINIFile(configPath, configSection, map[string]string{
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
