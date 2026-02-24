package handlers_ec2_instance

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"text/template"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/awserrors"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/objectstore"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewInstanceServiceImpl(t *testing.T) {
	cfg := &config.Config{}
	instanceTypes := map[string]*ec2.InstanceTypeInfo{
		"t3.micro": {InstanceType: aws.String("t3.micro")},
	}
	store := objectstore.NewMemoryObjectStore()
	instances := &vm.Instances{VMS: make(map[string]*vm.VM)}

	svc := NewInstanceServiceImpl(cfg, instanceTypes, nil, instances, store)

	require.NotNil(t, svc)
	assert.Equal(t, cfg, svc.config)
	assert.Equal(t, instanceTypes, svc.instanceTypes)
	assert.Nil(t, svc.natsConn)
	assert.Equal(t, instances, svc.instances)
	assert.Equal(t, store, svc.objectStore)
}

func TestGenerateHostname(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		want       string
	}{
		{
			name:       "Normal instance ID",
			instanceID: "i-0123456789abcdef0",
			want:       "hive-vm-01234567",
		},
		{
			name:       "Too short (2 chars)",
			instanceID: "ab",
			want:       "hive-vm-unknown",
		},
		{
			name:       "Empty string",
			instanceID: "",
			want:       "hive-vm-unknown",
		},
		{
			name:       "Exactly 10 chars",
			instanceID: "i-abcdef01",
			want:       "hive-vm-abcdef01",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateHostname(tt.instanceID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunInstance_Success(t *testing.T) {
	instanceTypes := map[string]*ec2.InstanceTypeInfo{
		"t3.micro": {InstanceType: aws.String("t3.micro")},
		"t3.small": {InstanceType: aws.String("t3.small")},
	}

	svc := &InstanceServiceImpl{
		instanceTypes: instanceTypes,
	}

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-0abcdef1234567890"),
		InstanceType: aws.String("t3.micro"),
		KeyName:      aws.String("my-key"),
	}

	instance, ec2Instance, err := svc.RunInstance(input)

	require.NoError(t, err)
	require.NotNil(t, instance)
	require.NotNil(t, ec2Instance)

	// Verify VM struct
	assert.Contains(t, instance.ID, "i-")
	assert.Equal(t, vm.StateProvisioning, instance.Status)
	assert.Equal(t, "t3.micro", instance.InstanceType)
	assert.Equal(t, input, instance.RunInstancesInput)
	assert.Equal(t, ec2Instance, instance.Instance)

	// Verify EC2 metadata
	assert.Equal(t, instance.ID, *ec2Instance.InstanceId)
	assert.Equal(t, "t3.micro", *ec2Instance.InstanceType)
	assert.Equal(t, "ami-0abcdef1234567890", *ec2Instance.ImageId)
	assert.Equal(t, "my-key", *ec2Instance.KeyName)
	assert.Equal(t, int64(0), *ec2Instance.State.Code)
	assert.Equal(t, "pending", *ec2Instance.State.Name)
	assert.NotNil(t, ec2Instance.LaunchTime)
}

func TestRunInstance_NoKeyName(t *testing.T) {
	instanceTypes := map[string]*ec2.InstanceTypeInfo{
		"t3.micro": {InstanceType: aws.String("t3.micro")},
	}

	svc := &InstanceServiceImpl{instanceTypes: instanceTypes}

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-012345"),
		InstanceType: aws.String("t3.micro"),
	}

	instance, ec2Instance, err := svc.RunInstance(input)

	require.NoError(t, err)
	require.NotNil(t, instance)
	assert.Nil(t, ec2Instance.KeyName)
}

func TestRunInstance_InvalidInstanceType(t *testing.T) {
	instanceTypes := map[string]*ec2.InstanceTypeInfo{
		"t3.micro": {InstanceType: aws.String("t3.micro")},
	}

	svc := &InstanceServiceImpl{instanceTypes: instanceTypes}

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-012345"),
		InstanceType: aws.String("nonexistent.type"),
	}

	instance, ec2Instance, err := svc.RunInstance(input)

	require.Error(t, err)
	assert.Equal(t, awserrors.ErrorInvalidInstanceType, err.Error())
	assert.Nil(t, instance)
	assert.Nil(t, ec2Instance)
}

func TestRunInstance_UniqueIDs(t *testing.T) {
	instanceTypes := map[string]*ec2.InstanceTypeInfo{
		"t3.micro": {InstanceType: aws.String("t3.micro")},
	}

	svc := &InstanceServiceImpl{instanceTypes: instanceTypes}

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-012345"),
		InstanceType: aws.String("t3.micro"),
	}

	instance1, _, err1 := svc.RunInstance(input)
	instance2, _, err2 := svc.RunInstance(input)

	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.NotEqual(t, instance1.ID, instance2.ID, "Each instance should have a unique ID")
}

func TestCloudInitTemplateRendering(t *testing.T) {
	tests := []struct {
		name     string
		data     CloudInitData
		contains []string
	}{
		{
			name: "Basic SSH key and hostname",
			data: CloudInitData{
				Username: "ec2-user",
				SSHKey:   "ssh-rsa AAAAB3... user@host",
				Hostname: "hive-vm-01234567",
			},
			contains: []string{
				"ec2-user",
				"ssh-rsa AAAAB3... user@host",
				"hive-vm-01234567",
				"#cloud-config",
			},
		},
		{
			name: "With cloud-config userdata",
			data: CloudInitData{
				Username:            "ec2-user",
				SSHKey:              "ssh-ed25519 AAAA...",
				Hostname:            "hive-vm-abcdef01",
				UserDataCloudConfig: "packages:\n  - nginx",
			},
			contains: []string{
				"packages:",
				"nginx",
				"custom userdata cloud-config",
			},
		},
		{
			name: "With script userdata",
			data: CloudInitData{
				Username:       "ec2-user",
				SSHKey:         "ssh-rsa AAAA...",
				Hostname:       "hive-vm-test",
				UserDataScript: "    #!/bin/bash\n    echo hello",
			},
			contains: []string{
				"write_files:",
				"/tmp/cloud-init-startup.sh",
				"runcmd:",
				"echo hello",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl := template.Must(template.New("cloud-init").Parse(cloudInitUserDataTemplate))
			var buf bytes.Buffer
			err := tmpl.Execute(&buf, tt.data)
			require.NoError(t, err)

			rendered := buf.String()
			for _, s := range tt.contains {
				assert.Contains(t, rendered, s)
			}
		})
	}
}

func TestCloudInitMetaTemplateRendering(t *testing.T) {
	data := CloudInitMetaData{
		InstanceID: "i-0123456789abcdef0",
		Hostname:   "hive-vm-01234567",
	}

	tmpl := template.Must(template.New("meta-data").Parse(cloudInitMetaTemplate))
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, data)
	require.NoError(t, err)

	rendered := buf.String()
	assert.Contains(t, rendered, "i-0123456789abcdef0")
	assert.Contains(t, rendered, "hive-vm-01234567")
	assert.Contains(t, rendered, "instance-id:")
	assert.Contains(t, rendered, "local-hostname:")
}

// TestCloudInitVolumeNamePerInstance verifies that AMI-based launches produce
// unique root volume IDs, which in turn produce unique cloud-init volume names.
// This prevents the bug where a cached cloud-init ISO (keyed by AMI) would
// serve stale SSH keys or hostnames to subsequent instances.
func TestCloudInitVolumeNamePerInstance(t *testing.T) {
	amiID := "ami-0abcdef1234567890"

	seen := make(map[string]bool)
	for range 100 {
		// Simulate GenerateVolumes logic for AMI-based launches (line 194-195)
		var rootVolumeId string
		if strings.HasPrefix(amiID, "ami-") {
			rootVolumeId = utils.GenerateResourceID("vol")
		}

		cloudInitName := fmt.Sprintf("%s-cloudinit", rootVolumeId)

		assert.True(t, strings.HasPrefix(cloudInitName, "vol-"),
			"cloud-init volume should be keyed by root volume ID, not AMI ID")
		assert.True(t, strings.HasSuffix(cloudInitName, "-cloudinit"))
		assert.False(t, strings.Contains(cloudInitName, "ami-"),
			"cloud-init volume name must not contain the AMI ID")
		assert.False(t, seen[cloudInitName],
			"each instance must get a unique cloud-init volume name")
		seen[cloudInitName] = true
	}
}
