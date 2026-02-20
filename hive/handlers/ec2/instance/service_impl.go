package handlers_ec2_instance

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	awss3 "github.com/aws/aws-sdk-go/service/s3"
	"github.com/kdomanski/iso9660"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/objectstore"
	"github.com/mulgadc/hive/hive/utils"
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

// VolumeInfo holds volume information returned from GenerateVolumes
// for populating BlockDeviceMappings in the EC2 API response
type VolumeInfo struct {
	VolumeId            string
	DeviceName          string
	AttachTime          time.Time
	DeleteOnTermination bool
}

// InstanceServiceImpl handles daemon-side EC2 instance operations
type InstanceServiceImpl struct {
	config        *config.Config
	instanceTypes map[string]*ec2.InstanceTypeInfo
	natsConn      *nats.Conn
	instances     *vm.Instances
	objectStore   objectstore.ObjectStore
}

// NewInstanceServiceImpl creates a new instance service implementation for daemon use
func NewInstanceServiceImpl(cfg *config.Config, instanceTypes map[string]*ec2.InstanceTypeInfo, nc *nats.Conn, instances *vm.Instances, store objectstore.ObjectStore) *InstanceServiceImpl {
	return &InstanceServiceImpl{
		config:        cfg,
		instanceTypes: instanceTypes,
		natsConn:      nc,
		instances:     instances,
		objectStore:   store,
	}
}

// RunInstance creates a single EC2 instance (called per-instance by daemon)
// Returns the VM struct and EC2 instance metadata
func (s *InstanceServiceImpl) RunInstance(input *ec2.RunInstancesInput) (*vm.VM, *ec2.Instance, error) {
	// Validate instance type exists
	_, exists := s.instanceTypes[*input.InstanceType]
	if !exists {
		return nil, nil, errors.New(awserrors.ErrorInvalidInstanceType)
	}

	instanceId := utils.GenerateResourceID("i")

	// Create new instance structure
	instance := &vm.VM{
		ID:           instanceId,
		Status:       vm.StateProvisioning,
		InstanceType: *input.InstanceType,
	}

	// Create EC2 instance metadata
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

	// Store EC2 API metadata in VM for DescribeInstances compatibility
	instance.RunInstancesInput = input
	instance.Instance = ec2Instance

	return instance, ec2Instance, nil
}

func (s *InstanceServiceImpl) GenerateVolumes(input *ec2.RunInstancesInput, instance *vm.VM) ([]VolumeInfo, error) {

	var size int = 4 * 1024 * 1024 * 1024 // 4GB default size
	var deviceName = "/dev/vda"           // Default device name (virtio-blk-pci)
	var volumeType string
	var iops int
	var imageId string
	var snapshotId string
	var deleteOnTermination = true // Default to true (matches AWS RunInstances behavior)

	// Handle block device mappings
	if len(input.BlockDeviceMappings) > 0 {
		bdm := input.BlockDeviceMappings[0]
		if bdm.DeviceName != nil {
			deviceName = *bdm.DeviceName
		}
		if bdm.Ebs != nil {
			if bdm.Ebs.VolumeSize != nil {
				size = int(*bdm.Ebs.VolumeSize) * 1024 * 1024 * 1024 // AWS API sends GiB, convert to bytes
			}
			if bdm.Ebs.VolumeType != nil {
				volumeType = *bdm.Ebs.VolumeType
			}
			if bdm.Ebs.Iops != nil {
				iops = int(*bdm.Ebs.Iops)
			}
			if bdm.Ebs.DeleteOnTermination != nil {
				deleteOnTermination = *bdm.Ebs.DeleteOnTermination
			}
		}
	}

	// Determine image ID and snapshot ID
	if strings.HasPrefix(*input.ImageId, "ami-") {
		imageId = utils.GenerateResourceID("vol")
		snapshotId = *input.ImageId
	} else {
		imageId = *input.ImageId
	}

	// Capture attach time for the root volume
	attachTime := time.Now()

	volumeConfig := viperblock.VolumeConfig{
		VolumeMetadata: viperblock.VolumeMetadata{
			VolumeID:            imageId,
			SizeGiB:             utils.SafeIntToUint64(size / 1024 / 1024 / 1024),
			CreatedAt:           attachTime,
			DeviceName:          deviceName,
			VolumeType:          volumeType,
			IOPS:                iops,
			SnapshotID:          snapshotId,
			DeleteOnTermination: deleteOnTermination,
		},
	}

	// Step 1: Create or validate root volume
	err := s.prepareRootVolume(input, imageId, size, volumeConfig, instance, deleteOnTermination)
	if err != nil {
		return nil, err
	}

	// Step 2: Create EFI partition
	err = s.prepareEFIVolume(imageId, volumeConfig, instance)
	if err != nil {
		return nil, err
	}

	// Step 3: Create cloud-init volume if needed
	if input.KeyName != nil && *input.KeyName != "" || (input.UserData != nil && *input.UserData != "") {
		err = s.prepareCloudInitVolume(input, imageId, volumeConfig, instance)
		if err != nil {
			return nil, err
		}
	}

	// Return volume info for the root volume only (EFI and cloud-init are internal)
	volumeInfos := []VolumeInfo{
		{
			VolumeId:            imageId,
			DeviceName:          deviceName,
			AttachTime:          attachTime,
			DeleteOnTermination: deleteOnTermination,
		},
	}

	return volumeInfos, nil

}

// newViperblock creates a viperblock instance with the service's S3/Predastore credentials.
func (s *InstanceServiceImpl) newViperblock(volumeName string, size int, volumeConfig viperblock.VolumeConfig) (*viperblock.VB, error) {
	cfg := s3.S3Config{
		VolumeName: volumeName,
		VolumeSize: utils.SafeIntToUint64(size),
		Bucket:     s.config.Predastore.Bucket,
		Region:     s.config.Predastore.Region,
		AccessKey:  s.config.AccessKey,
		SecretKey:  s.config.SecretKey,
		Host:       s.config.Predastore.Host,
	}

	vbconfig := viperblock.VB{
		VolumeName:   volumeName,
		VolumeSize:   utils.SafeIntToUint64(size),
		BaseDir:      s.config.WalDir,
		Cache:        viperblock.Cache{Config: viperblock.CacheConfig{Size: 0}},
		VolumeConfig: volumeConfig,
	}

	return viperblock.New(&vbconfig, "s3", cfg)
}

// prepareRootVolume handles creation/cloning of the root volume
func (s *InstanceServiceImpl) prepareRootVolume(input *ec2.RunInstancesInput, imageId string, size int, volumeConfig viperblock.VolumeConfig, instance *vm.VM, deleteOnTermination bool) error {
	vb, err := s.newViperblock(imageId, size, volumeConfig)
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
		Name:                imageId,
		Boot:                true,
		DeleteOnTermination: deleteOnTermination,
	})
	instance.EBSRequests.Mu.Unlock()

	return nil
}

// cloneAMIToVolume creates a new volume from an AMI using snapshot-based
// zero-copy cloning. The destination volume points at the AMI's frozen block
// map and reads on-demand from the AMI's chunks (copy-on-write).
func (s *InstanceServiceImpl) cloneAMIToVolume(input *ec2.RunInstancesInput, size int, volumeConfig viperblock.VolumeConfig, destVb *viperblock.VB) error {
	// Load AMI state to get the snapshot ID
	amiVb, err := s.newViperblock(*input.ImageId, size, volumeConfig)
	if err != nil {
		slog.Error("Failed to connect to Viperblock store for AMI", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	err = amiVb.Backend.Init()
	if err != nil {
		slog.Error("Could not connect to AMI Viperblock store", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	amiState, err := amiVb.LoadStateRequest("")
	if err != nil {
		slog.Error("Could not load state for AMI", "imageId", *input.ImageId, "err", err)
		return errors.New(awserrors.ErrorInvalidAMIIDNotFound)
	}

	snapshotID := amiState.VolumeConfig.AMIMetadata.SnapshotID
	if snapshotID == "" {
		slog.Error("AMI has no snapshot ID, cannot perform zero-copy clone", "imageId", *input.ImageId)
		return errors.New(awserrors.ErrorServerInternal)
	}

	slog.Info("Cloning AMI via snapshot", "imageId", *input.ImageId, "snapshotID", snapshotID)

	// Set up destination volume from the snapshot (zero-copy)
	err = destVb.OpenFromSnapshot(snapshotID)
	if err != nil {
		slog.Error("Failed to open from snapshot", "snapshotID", snapshotID, "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	// Persist the snapshot relationship to the backend
	err = destVb.SaveState()
	if err != nil {
		slog.Error("Failed to save state", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	err = destVb.SaveBlockState()
	if err != nil {
		slog.Error("Failed to save block state", "err", err)
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

	// Update VolumeID to match the EFI volume name
	efiVolumeConfig := volumeConfig
	efiVolumeConfig.VolumeMetadata.VolumeID = efiVolumeName

	efiVb, err := s.newViperblock(efiVolumeName, efiSize, efiVolumeConfig)
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

		// Open the chunk WAL (sharded or legacy)
		if efiVb.UseShardedWAL {
			err = efiVb.OpenShardedWAL()
		} else {
			err = efiVb.OpenWAL(&efiVb.WAL, fmt.Sprintf("%s/%s", efiVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALChunk, efiVb.WAL.WallNum.Load(), efiVb.GetVolume())))
		}
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
		if err := efiVb.WriteAt(0, make([]byte, efiVb.BlockSize)); err != nil {
			slog.Error("Failed to write empty EFI block", "err", err)
		}
		if err := efiVb.Flush(); err != nil {
			slog.Error("Failed to flush EFI volume", "err", err)
		}
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

	// Update VolumeID to match the cloud-init volume name
	cloudInitVolumeConfig := volumeConfig
	cloudInitVolumeConfig.VolumeMetadata.VolumeID = cloudInitVolumeName

	cloudInitVb, err := s.newViperblock(cloudInitVolumeName, cloudInitSize, cloudInitVolumeConfig)
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

		// Open the chunk WAL (sharded or legacy)
		if cloudInitVb.UseShardedWAL {
			err = cloudInitVb.OpenShardedWAL()
		} else {
			err = cloudInitVb.OpenWAL(&cloudInitVb.WAL, fmt.Sprintf("%s/%s", cloudInitVb.WAL.BaseDir, types.GetFilePath(types.FileTypeWALChunk, cloudInitVb.WAL.WallNum.Load(), cloudInitVb.GetVolume())))
		}
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
		Name:      cloudInitVolumeName,
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
	keyName := ""
	if input.KeyName != nil {
		keyName = *input.KeyName
	}

	// TODO: Mock for account ID, replace with real account ID retrieval
	keyPath := fmt.Sprintf("/keys/123456789/%s", keyName)
	result, err := s.objectStore.GetObject(&awss3.GetObjectInput{
		Bucket: aws.String(s.config.Predastore.Bucket),
		Key:    aws.String(keyPath),
	})
	if err != nil {
		if objectstore.IsNoSuchKeyError(err) {
			slog.Error("key pair not found", "keyName", keyName, "err", err)
			return errors.New(awserrors.ErrorInvalidKeyPairNotFound)
		}
		slog.Error("failed to read SSH key", "err", err)
		return errors.New(awserrors.ErrorServerInternal)
	}

	sshKey, err := io.ReadAll(result.Body)
	if err != nil {
		slog.Error("failed to read SSH key body", "err", err)
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

	outputFile, err := os.OpenFile(tempFile.Name(), os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0600)
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
	if err := cloudInitVb.Flush(); err != nil {
		slog.Error("Failed to flush cloud-init volume", "err", err)
	}
	if err := cloudInitVb.WriteWALToChunk(true); err != nil {
		slog.Error("Failed to write WAL to chunk", "err", err)
	}

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
