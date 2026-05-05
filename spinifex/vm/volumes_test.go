package vm

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNextAvailableDevice(t *testing.T) {
	tests := []struct {
		name       string
		instance   *VM
		wantDevice string
	}{
		{
			name: "empty instance returns first device",
			instance: &VM{
				Instance: &ec2.Instance{},
			},
			wantDevice: "/dev/sdf",
		},
		{
			name:       "nil Instance returns first device",
			instance:   &VM{},
			wantDevice: "/dev/sdf",
		},
		{
			name: "existing BlockDeviceMappings skipped",
			instance: &VM{
				Instance: &ec2.Instance{
					BlockDeviceMappings: []*ec2.InstanceBlockDeviceMapping{
						{DeviceName: aws.String("/dev/sdf")},
						{DeviceName: aws.String("/dev/sdg")},
					},
				},
			},
			wantDevice: "/dev/sdh",
		},
		{
			name: "existing EBSRequests skipped",
			instance: &VM{
				Instance: &ec2.Instance{},
				EBSRequests: types.EBSRequests{
					Requests: []types.EBSRequest{
						{Name: "vol-1", DeviceName: "/dev/sdf"},
					},
				},
			},
			wantDevice: "/dev/sdg",
		},
		{
			name: "mixed sources all skipped",
			instance: &VM{
				Instance: &ec2.Instance{
					BlockDeviceMappings: []*ec2.InstanceBlockDeviceMapping{
						{DeviceName: aws.String("/dev/sdf")},
					},
				},
				EBSRequests: types.EBSRequests{
					Requests: []types.EBSRequest{
						{Name: "vol-1", DeviceName: "/dev/sdg"},
					},
				},
			},
			wantDevice: "/dev/sdh",
		},
		{
			name: "all devices f-p used returns empty",
			instance: func() *VM {
				var bdms []*ec2.InstanceBlockDeviceMapping
				for c := 'f'; c <= 'p'; c++ {
					dev := fmt.Sprintf("/dev/sd%c", c)
					bdms = append(bdms, &ec2.InstanceBlockDeviceMapping{
						DeviceName: aws.String(dev),
					})
				}
				return &VM{
					Instance: &ec2.Instance{BlockDeviceMappings: bdms},
				}
			}(),
			wantDevice: "",
		},
		{
			name: "nil DeviceName in BlockDeviceMappings ignored",
			instance: &VM{
				Instance: &ec2.Instance{
					BlockDeviceMappings: []*ec2.InstanceBlockDeviceMapping{
						{DeviceName: nil},
						{DeviceName: aws.String("/dev/sdf")},
					},
				},
			},
			wantDevice: "/dev/sdg",
		},
		{
			name: "empty DeviceName in EBSRequests ignored",
			instance: &VM{
				EBSRequests: types.EBSRequests{
					Requests: []types.EBSRequest{
						{DeviceName: ""},
						{DeviceName: "/dev/sdf"},
					},
				},
			},
			wantDevice: "/dev/sdg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nextAvailableDevice(tt.instance)
			assert.Equal(t, tt.wantDevice, got)
		})
	}
}

func TestIsQMPDeviceNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "matches DeviceNotFound", err: errors.New("QMP error: DeviceNotFound: device 'vdisk-x' not found"), want: true},
		{name: "other QMP error", err: errors.New("QMP error: GenericError: bad command"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isQMPDeviceNotFound(tt.err))
		})
	}
}

func TestIsQMPNodeInUse(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "matches 'is in use'", err: errors.New("QMP error: GenericError: Node 'nbd-x' is in use"), want: true},
		{name: "matches 'still in use'", err: errors.New("QMP error: GenericError: Node 'nbd-x' is still in use"), want: true},
		{name: "other QMP error", err: errors.New("QMP error: GenericError: not found"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isQMPNodeInUse(tt.err))
		})
	}
}

func TestAttachVolume_InstanceNotFound(t *testing.T) {
	m := NewManager()
	_, err := m.AttachVolume("i-missing", "vol-1", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInstanceNotFound)
}

func TestAttachVolume_NotRunning(t *testing.T) {
	m := NewManager()
	m.Insert(&VM{ID: "i-1", Status: StateStopped, Instance: &ec2.Instance{}})

	_, err := m.AttachVolume("i-1", "vol-1", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidTransition)
}

func TestAttachVolume_AttachmentLimitExceeded(t *testing.T) {
	bdms := make([]*ec2.InstanceBlockDeviceMapping, 0, 11)
	for c := 'f'; c <= 'p'; c++ {
		dev := fmt.Sprintf("/dev/sd%c", c)
		bdms = append(bdms, &ec2.InstanceBlockDeviceMapping{DeviceName: aws.String(dev)})
	}
	m := NewManager()
	m.Insert(&VM{ID: "i-1", Status: StateRunning, Instance: &ec2.Instance{BlockDeviceMappings: bdms}})

	_, err := m.AttachVolume("i-1", "vol-1", "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAttachmentLimitExceeded)
}

func TestDetachVolume_InstanceNotFound(t *testing.T) {
	m := NewManager()
	_, err := m.DetachVolume("i-missing", "vol-1", "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInstanceNotFound)
}

func TestDetachVolume_NotRunning(t *testing.T) {
	m := NewManager()
	m.Insert(&VM{ID: "i-1", Status: StateStopped})

	_, err := m.DetachVolume("i-1", "vol-1", "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidTransition)
}

func TestDetachVolume_VolumeNotAttached(t *testing.T) {
	m := NewManager()
	m.Insert(&VM{ID: "i-1", Status: StateRunning, Instance: &ec2.Instance{}})

	_, err := m.DetachVolume("i-1", "vol-missing", "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrVolumeNotAttached)
}

func TestDetachVolume_BootVolumeRejected(t *testing.T) {
	m := NewManager()
	m.Insert(&VM{
		ID:       "i-1",
		Status:   StateRunning,
		Instance: &ec2.Instance{},
		EBSRequests: types.EBSRequests{
			Requests: []types.EBSRequest{{Name: "vol-boot", Boot: true}},
		},
	})

	_, err := m.DetachVolume("i-1", "vol-boot", "", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrVolumeNotDetachable)
}

func TestDetachVolume_DeviceMismatch(t *testing.T) {
	m := NewManager()
	m.Insert(&VM{
		ID:       "i-1",
		Status:   StateRunning,
		Instance: &ec2.Instance{},
		EBSRequests: types.EBSRequests{
			Requests: []types.EBSRequest{{Name: "vol-1", DeviceName: "/dev/sdf"}},
		},
	})

	_, err := m.DetachVolume("i-1", "vol-1", "/dev/sdg", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrVolumeDeviceMismatch)
}

func TestReboot_InstanceNotFound(t *testing.T) {
	m := NewManager()
	err := m.Reboot("i-missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInstanceNotFound)
}

func TestReboot_NotRunning(t *testing.T) {
	m := NewManager()
	m.Insert(&VM{ID: "i-1", Status: StateStopped})

	err := m.Reboot("i-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidTransition)
}
