package handlers_ec2_instance

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/kdomanski/iso9660"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/s3client"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/mulgadc/viperblock/types"
	"github.com/mulgadc/viperblock/viperblock"
	"github.com/mulgadc/viperblock/viperblock/backends/s3"
	"github.com/nats-io/nats.go"
)

const cloudInitUserDataTemplate = `#cloud-config
users:
  - name: {{.Username}}
    shell: /bin/bash
    groups:
      - sudo
    sudo: "ALL=(ALL) NOPASSWD:ALL"
    ssh_authorized_keys:
      - {{.SSHKey}}

hostname: {{.Hostname}}
manage_etc_hosts: true

{{if .UserDataCloudConfig}}

# custom userdata cloud-config
{{.UserDataCloudConfig}}

{{end}}

{{if .UserDataScript}}
write_files:
  - path: /tmp/cloud-init-startup.sh
    permissions: '0755'
    content: |
{{.UserDataScript}}

runcmd:
  - [ "/bin/bash", "/tmp/cloud-init-startup.sh" ]
{{end}}
`

const cloudInitMetaTemplate = `# meta-data
instance-id: {{.InstanceID}}
local-hostname: {{.Hostname}}
`

type CloudInitData struct {
	Username            string
	SSHKey              string
	Hostname            string
	UserDataCloudConfig string
	UserDataScript      string
}

type CloudInitMetaData struct {
	InstanceID string
	Hostname   string
}

// InstanceType represents the resource requirements for an EC2 instance type
type InstanceType struct {
	Name         string
	VCPUs        int
	MemoryGB     float64
	Architecture string // e.g., "x86_64", "arm64"
}

// InstanceServiceImpl handles daemon-side EC2 instance operations
type InstanceServiceImpl struct {
	config        *config.Config
	instanceTypes map[string]InstanceType
	natsConn      *nats.Conn
	instances     *vm.Instances
}

// NewInstanceServiceImpl creates a new instance service implementation for daemon use
func NewInstanceServiceImpl(cfg *config.Config, instanceTypes map[string]InstanceType, nc *nats.Conn, instances *vm.Instances) *InstanceServiceImpl {
	return &InstanceServiceImpl{
		config:        cfg,
		instanceTypes: instanceTypes,
		natsConn:      nc,
		instances:     instances,
	}
}

// RunInstances handles the business logic for launching EC2 instances
// This prepares all volumes (root, EFI, cloud-init) and returns the instance ready to launch
func (s *InstanceServiceImpl) RunInstances(input *ec2.RunInstancesInput) (*vm.VM, ec2.Reservation, error) {
	var reservation ec2.Reservation

	// Validate input (validation is already done in daemon.handleEC2Launch)
	// We can skip re-validation here for performance

	// Validate instance type exists
	_, exists := s.instanceTypes[*input.InstanceType]
	if !exists {
		return nil, reservation, errors.New(awserrors.ErrorInvalidInstanceType)
	}

	instanceId := vm.GenerateEC2InstanceID()

	// Create new instance structure
	instance := &vm.VM{
		ID:           instanceId,
		Status:       "provisioning",
		InstanceType: *input.InstanceType,
	}

	// Create reservation response
	reservation.SetReservationId(vm.GenerateEC2ReservationID())
	reservation.SetOwnerId("123456789012") // TODO: Use actual owner ID from config

	// TODO: Loop through multiple instance creation based on MinCount / MaxCount
	reservation.Instances = make([]*ec2.Instance, 1)
	ec2Instance := &ec2.Instance{
		State: &ec2.InstanceState{},
	}
	ec2Instance.SetInstanceId(instance.ID)
	ec2Instance.SetInstanceType(*input.InstanceType)
	ec2Instance.SetImageId(*input.ImageId)
	if input.KeyName != nil {
		ec2Instance.SetKeyName(*input.KeyName)
	}
	ec2Instance.SetLaunchTime(time.Now())
	ec2Instance.State.SetCode(0)
	ec2Instance.State.SetName("pending")

	reservation.Instances[0] = ec2Instance

	// Store EC2 API metadata in VM for DescribeInstances compatibility
	instance.RunInstancesInput = input
	instance.Reservation = &reservation
	instance.Instance = ec2Instance

	// Return instance attributes, defer disk preparation to later step

	return instance, reservation, nil
}

func (s *InstanceServiceImpl) GenerateVolumes(input *ec2.RunInstancesInput, instance *vm.VM) (err error) {

	var size int = 4 * 1024 * 1024 * 1024 // 4GB default size
	var deviceName string
	var volumeType string
	var iops int
	var imageId string
	var snapshotId string

	// Handle block device mappings
	if input.BlockDeviceMappings != nil && len(input.BlockDeviceMappings) > 0 {
		size = int(*input.BlockDeviceMappings[0].Ebs.VolumeSize)
		deviceName = *input.BlockDeviceMappings[0].DeviceName
		volumeType = *input.BlockDeviceMappings[0].Ebs.VolumeType
		iops = int(*input.BlockDeviceMappings[0].Ebs.Iops)
	}

	// Determine image ID and snapshot ID
	if strings.HasPrefix(*input.ImageId, "ami-") {
		randomNumber := rand.Intn(100_000_000)
		imageId = viperblock.GenerateVolumeID("vol", fmt.Sprintf("%d-%s", randomNumber, *input.ImageId), "predastore", time.Now().Unix())
		snapshotId = *input.ImageId
	} else {
		imageId = *input.ImageId
	}

	volumeConfig := viperblock.VolumeConfig{
		VolumeMetadata: viperblock.VolumeMetadata{
			VolumeID:   imageId,
			SizeGiB:    uint64(size / 1024 / 1024 / 1024),
			CreatedAt:  time.Now(),
			DeviceName: deviceName,
			VolumeType: volumeType,
			IOPS:       iops,
			SnapshotID: snapshotId,
		},
	}

	// Step 1: Create or validate root volume
	err = s.prepareRootVolume(input, imageId, size, volumeConfig, instance)
	if err != nil {
		return err
	}

	// Step 2: Create EFI partition
	err = s.prepareEFIVolume(imageId, volumeConfig, instance)
	if err != nil {
		return err
	}

	// Step 3: Create cloud-init volume if needed
	if input.KeyName != nil && *input.KeyName != "" || (input.UserData != nil && *input.UserData != "") {
		err = s.prepareCloudInitVolume(input, imageId, volumeConfig, instance)
		if err != nil {
			return err
		}
	}

	return nil

}

// prepareRootVolume handles creation/cloning of the root volume
func (s *InstanceServiceImpl) prepareRootVolume(input *ec2.RunInstancesInput, imageId string, size int, volumeConfig viperblock.VolumeConfig, instance *vm.VM) error {
	cfg := s3.S3Config{
		VolumeName: imageId,
		VolumeSize: uint64(size),
		Bucket:     s.config.Predastore.Bucket,
		Region:     s.config.Predastore.Region,
		AccessKey:  s.config.AccessKey,
		SecretKey:  s.config.SecretKey,
		Host:       s.config.Predastore.Host,
	}

	vbconfig := viperblock.VB{
		VolumeName:   imageId,
		VolumeSize:   uint64(size),
		BaseDir:      s.config.WalDir,
		Cache:        viperblock.Cache{Config: viperblock.CacheConfig{Size: 0}},
		VolumeConfig: volumeConfig,
	}

	vb, err := viperblock.New(vbconfig, "s3", cfg)
	if err != nil {
		slog.Error("Failed to connect to Viperblock store", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	vb.SetDebug(false)

	// Initialize the backend
	err = vb.Backend.Init()
	if err != nil {
		slog.Error("Failed to initialize backend", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	// Load the state from the remote backend
	_, err = vb.LoadStateRequest("")

	// If volume doesn't exist, clone from AMI
	if err != nil {
		slog.Info("Volume does not yet exist, creating from AMI ...")

		err = s.cloneAMIToVolume(input, size, volumeConfig, vb)
		if err != nil {
			return err
		}
	}

	// Append root volume to instance
	instance.EBSRequests.Mu.Lock()
	instance.EBSRequests.Requests = append(instance.EBSRequests.Requests, config.EBSRequest{
		Name: vbconfig.VolumeName,
		Boot: true,
	})
	instance.EBSRequests.Mu.Unlock()

	return nil
}

// cloneAMIToVolume clones an AMI to a new volume
func (s *InstanceServiceImpl) cloneAMIToVolume(input *ec2.RunInstancesInput, size int, volumeConfig viperblock.VolumeConfig, destVb *viperblock.VB) error {
	// Open WALs for destination volume
	err := destVb.OpenWAL(&destVb.WAL, fmt.Sprintf("%s/%s", destVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALChunk, destVb.WAL.WallNum.Load(), destVb.GetVolume())))
	if err != nil {
		slog.Error("Failed to load WAL", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	err = destVb.OpenWAL(&destVb.BlockToObjectWAL, fmt.Sprintf("%s/%s", destVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALBlock, destVb.BlockToObjectWAL.WallNum.Load(), destVb.GetVolume())))
	if err != nil {
		slog.Error("Failed to load block WAL", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	// Setup source AMI Viperblock
	amiCfg := s3.S3Config{
		VolumeName: *input.ImageId,
		VolumeSize: uint64(size),
		Bucket:     s.config.Predastore.Bucket,
		Region:     s.config.Predastore.Region,
		AccessKey:  s.config.AccessKey,
		SecretKey:  s.config.SecretKey,
		Host:       s.config.Predastore.Host,
	}

	amiVbConfig := viperblock.VB{
		VolumeName:   *input.ImageId,
		VolumeSize:   uint64(size),
		BaseDir:      s.config.WalDir,
		Cache:        viperblock.Cache{Config: viperblock.CacheConfig{Size: 0}},
		VolumeConfig: volumeConfig,
	}

	amiVb, err := viperblock.New(amiVbConfig, "s3", amiCfg)
	if err != nil {
		slog.Error("Failed to connect to Viperblock store for AMI", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	// Initialize AMI backend
	slog.Debug("Initializing AMI Viperblock store backend")
	err = amiVb.Backend.Init()
	if err != nil {
		slog.Error("Could not connect to AMI Viperblock store", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	slog.Debug("Loading state for AMI Viperblock store")
	err = amiVb.LoadState()
	if err != nil {
		slog.Error("Could not load state for AMI Viperblock store", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	err = amiVb.LoadBlockState()
	if err != nil {
		slog.Error("Failed to load block state", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	slog.Debug("Starting to clone AMI to new volume")

	// Clone blocks from AMI to new volume
	var block uint64 = 0
	nullBlock := make([]byte, destVb.BlockSize)

	for {
		if block*uint64(destVb.BlockSize) >= amiVb.VolumeSize {
			slog.Debug("Reached end of AMI")
			break
		}

		// Read 1MB
		data, err := amiVb.ReadAt(block*uint64(destVb.BlockSize), uint64(destVb.BlockSize)*1024)
		if err != nil && err != viperblock.ZeroBlock {
			slog.Error("Failed to read block from AMI source", "err", err)
			return errors.New(awserrors.ErrorServerInternal)
		}

		numBlocks := len(data) / int(destVb.BlockSize)

		// Write individual blocks to the new volume
		for i := 0; i < numBlocks; i++ {
			// Check if the input is a Zero block
			if bytes.Equal(data[i*int(destVb.BlockSize):(i+1)*int(destVb.BlockSize)], nullBlock) {
				block++
				continue
			}

			destVb.WriteAt(block*uint64(destVb.BlockSize), data[i*int(destVb.BlockSize):(i+1)*int(destVb.BlockSize)])
			block++

			// Flush every 4MB
			if block%uint64(destVb.BlockSize) == 0 {
				// slog.Debug("Flush", "block", block)
				destVb.Flush()
				destVb.WriteWALToChunk(true)
			}
		}
	}

	err = destVb.Close()
	if err != nil {
		slog.Error("Failed to close Viperblock store", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	err = destVb.RemoveLocalFiles()
	if err != nil {
		slog.Warn("Failed to remove local files", "err", err)
	}

	return nil
}

// prepareEFIVolume creates the EFI boot partition
func (s *InstanceServiceImpl) prepareEFIVolume(imageId string, volumeConfig viperblock.VolumeConfig, instance *vm.VM) error {
	efiVolumeName := fmt.Sprintf("%s-efi", imageId)
	efiSize := 64 * 1024 * 1024 // 64MB

	efiCfg := s3.S3Config{
		VolumeName: efiVolumeName,
		VolumeSize: uint64(efiSize),
		Bucket:     s.config.Predastore.Bucket,
		Region:     s.config.Predastore.Region,
		AccessKey:  s.config.AccessKey,
		SecretKey:  s.config.SecretKey,
		Host:       s.config.Predastore.Host,
	}

	efiVbConfig := viperblock.VB{
		VolumeName:   efiVolumeName,
		VolumeSize:   uint64(efiSize),
		BaseDir:      s.config.WalDir,
		Cache:        viperblock.Cache{Config: viperblock.CacheConfig{Size: 0}},
		VolumeConfig: volumeConfig,
	}

	efiVb, err := viperblock.New(efiVbConfig, "s3", efiCfg)
	if err != nil {
		slog.Error("Could not create EFI viperblock", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	efiVb.SetDebug(false)

	// Initialize the backend
	slog.Debug("Initializing EFI Viperblock store backend")
	err = efiVb.Backend.Init()
	if err != nil {
		slog.Error("Failed to initialize EFI Viperblock store backend", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	// Load the state from the remote backend
	_, err = efiVb.LoadStateRequest("")
	slog.Info("LoadStateRequest", "error", err)

	// Create EFI volume if it doesn't exist
	if err != nil {
		slog.Info("Volume does not yet exist, creating EFI disk ...")

		// Open the chunk WAL
		err = efiVb.OpenWAL(&efiVb.WAL, fmt.Sprintf("%s/%s", efiVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALChunk, efiVb.WAL.WallNum.Load(), efiVb.GetVolume())))
		if err != nil {
			slog.Error("Failed to load WAL", "err", err)
			return errors.New(awserrors.ErrorServerInternal)
		}

		// Open the block to object WAL
		err = efiVb.OpenWAL(&efiVb.BlockToObjectWAL, fmt.Sprintf("%s/%s", efiVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALBlock, efiVb.BlockToObjectWAL.WallNum.Load(), efiVb.GetVolume())))
		if err != nil {
			slog.Error("Failed to load block WAL", "err", err)
			return errors.New(awserrors.ErrorServerInternal)
		}

		// Write an empty block to the EFI volume
		efiVb.WriteAt(0, make([]byte, efiVb.BlockSize))
		efiVb.Flush()
	}

	slog.Info("Closing EFI")
	err = efiVb.Close()
	slog.Info("Close", "error", err)
	if err != nil {
		slog.Error("Failed to close EFI Viperblock store", "err", err)
	}

	err = efiVb.RemoveLocalFiles()
	if err != nil {
		slog.Error("Failed to remove local files", "err", err)
	}

	instance.EBSRequests.Mu.Lock()
	instance.EBSRequests.Requests = append(instance.EBSRequests.Requests, config.EBSRequest{
		Name: efiVb.VolumeName,
		Boot: false,
		EFI:  true,
	})
	instance.EBSRequests.Mu.Unlock()

	return nil
}

// prepareCloudInitVolume creates cloud-init ISO with SSH keys and user data
func (s *InstanceServiceImpl) prepareCloudInitVolume(input *ec2.RunInstancesInput, imageId string, volumeConfig viperblock.VolumeConfig, instance *vm.VM) error {
	slog.Info("Creating cloud-init volume")

	cloudInitVolumeName := fmt.Sprintf("%s-cloudinit", imageId)
	cloudInitSize := 1 * 1024 * 1024 // 1MB

	cloudInitCfg := s3.S3Config{
		VolumeName: cloudInitVolumeName,
		VolumeSize: uint64(cloudInitSize),
		Bucket:     s.config.Predastore.Bucket,
		Region:     s.config.Predastore.Region,
		AccessKey:  s.config.AccessKey,
		SecretKey:  s.config.SecretKey,
		Host:       s.config.Predastore.Host,
	}

	cloudInitVbConfig := viperblock.VB{
		VolumeName:   cloudInitVolumeName,
		VolumeSize:   uint64(cloudInitSize),
		BaseDir:      s.config.WalDir,
		Cache:        viperblock.Cache{Config: viperblock.CacheConfig{Size: 0}},
		VolumeConfig: volumeConfig,
	}

	cloudInitVb, err := viperblock.New(cloudInitVbConfig, "s3", cloudInitCfg)
	if err != nil {
		slog.Error("Could not create cloudinit viperblock", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	cloudInitVb.SetDebug(false)

	// Initialize the backend
	slog.Debug("Initializing cloud-init Viperblock store backend")
	err = cloudInitVb.Backend.Init()
	if err != nil {
		slog.Error("Could not init backend", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	// Load the state from the remote backend
	_, err = cloudInitVb.LoadStateRequest("")

	// Create cloud-init volume if it doesn't exist
	if err != nil {
		slog.Info("Volume does not yet exist, creating cloud-init disk ...")

		// Open WALs
		err = cloudInitVb.OpenWAL(&cloudInitVb.WAL, fmt.Sprintf("%s/%s", cloudInitVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALChunk, cloudInitVb.WAL.WallNum.Load(), cloudInitVb.GetVolume())))
		if err != nil {
			slog.Error("Failed to load WAL", "err", err)
			return errors.New(awserrors.ErrorServerInternal)
		}

		err = cloudInitVb.OpenWAL(&cloudInitVb.BlockToObjectWAL, fmt.Sprintf("%s/%s", cloudInitVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALBlock, cloudInitVb.BlockToObjectWAL.WallNum.Load(), cloudInitVb.GetVolume())))
		if err != nil {
			slog.Error("Failed to load block WAL", "err", err)
			return errors.New(awserrors.ErrorServerInternal)
		}

		// Create the cloud-init ISO
		err = s.createCloudInitISO(input, instance.ID, cloudInitVb)
		if err != nil {
			return err
		}
	}

	err = cloudInitVb.Close()
	if err != nil {
		slog.Error("Failed to close cloud-init Viperblock store", "err", err)
	}

	err = cloudInitVb.RemoveLocalFiles()
	if err != nil {
		slog.Error("Failed to remove local files", "err", err)
	}

	instance.EBSRequests.Mu.Lock()
	instance.EBSRequests.Requests = append(instance.EBSRequests.Requests, config.EBSRequest{
		Name:      cloudInitCfg.VolumeName,
		Boot:      false,
		CloudInit: true,
	})
	instance.EBSRequests.Mu.Unlock()

	return nil
}

// createCloudInitISO generates the cloud-init ISO image
func (s *InstanceServiceImpl) createCloudInitISO(input *ec2.RunInstancesInput, instanceId string, cloudInitVb *viperblock.VB) error {
	// Create ISO writer
	writer, err := iso9660.NewWriter()
	if err != nil {
		slog.Error("failed to create writer", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}
	defer writer.Cleanup()

	// Generate instance metadata
	hostname := generateHostname(instanceId)

	// Retrieve SSH pubkey from S3
	s3c := s3client.New(s3client.S3Config{
		AccessKey: s.config.AccessKey,
		SecretKey: s.config.SecretKey,
		Host:      s.config.Predastore.Host,
		Bucket:    s.config.Predastore.Bucket,
		Region:    s.config.Predastore.Region,
	})

	err = s3c.Init()
	if err != nil {
		slog.Error("failed to initialize S3 client", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	keyName := ""
	if input.KeyName != nil {
		keyName = *input.KeyName
	}

	// TODO: Mock for account ID, replace with real account ID retrieval
	sshKey, err := s3c.Read(fmt.Sprintf("/keys/123456789/%s", keyName))
	if err != nil {
		slog.Error("failed to read SSH key", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	userData := CloudInitData{
		Username: "ec2-user",
		SSHKey:   string(sshKey),
		Hostname: hostname,
	}

	var buf bytes.Buffer
	t := template.Must(template.New("cloud-init").Parse(cloudInitUserDataTemplate))

	if err := t.Execute(&buf, userData); err != nil {
		slog.Error("failed to render template", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	// Add user-data
	err = writer.AddFile(&buf, "user-data")
	if err != nil {
		slog.Error("failed to add file", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	// Add meta-data
	metaData := CloudInitMetaData{
		InstanceID: instanceId,
		Hostname:   hostname,
	}

	t = template.Must(template.New("meta-data").Parse(cloudInitMetaTemplate))
	buf.Reset()

	if err := t.Execute(&buf, metaData); err != nil {
		slog.Error("failed to render template", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	err = writer.AddFile(&buf, "meta-data")
	if err != nil {
		slog.Error("failed to add file", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	// Store temp file
	tempFile, err := os.CreateTemp("", "cloud-init-*.iso")
	if err != nil {
		slog.Error("Could not create cloud-init temp file", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("Created temp ISO file", "file", tempFile.Name())

	outputFile, err := os.OpenFile(tempFile.Name(), os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0644)
	if err != nil {
		slog.Error("failed to create file", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	// Requires cidata volume label for cloud-init to recognize
	err = writer.WriteTo(outputFile, "cidata")
	if err != nil {
		slog.Error("failed to write ISO image", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	err = writer.Cleanup()
	if err != nil {
		slog.Error("failed to cleanup writer", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	err = outputFile.Close()
	if err != nil {
		slog.Error("failed to close output file", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	isoData, err := os.ReadFile(tempFile.Name())
	if err != nil {
		slog.Error("failed to read ISO image:", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	err = cloudInitVb.WriteAt(0, isoData)
	if err != nil {
		slog.Error("failed to write ISO image to viperblock volume", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	// Flush
	cloudInitVb.Flush()
	cloudInitVb.WriteWALToChunk(true)

	// Remove the temp ISO file
	err = os.Remove(tempFile.Name())
	if err != nil {
		slog.Error("Failed to remove temp file", "err", err)
	}

	return nil
}

// generateHostname creates a hostname based on instance ID
func generateHostname(instanceID string) string {
	if len(instanceID) > 2 {
		uniquePart := instanceID[2:10] // Take first 8 chars after "i-"
		return fmt.Sprintf("hive-vm-%s", uniquePart)
	}
	return "hive-vm-unknown"
}
