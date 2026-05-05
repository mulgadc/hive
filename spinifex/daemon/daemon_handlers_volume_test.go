package daemon

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mulgadc/spinifex/spinifex/awserrors"
	"github.com/mulgadc/spinifex/spinifex/vm"
	"github.com/stretchr/testify/assert"
)

// TestAttachDetachErrorCode locks down the manager-error → AWS-API-code
// mapping that handleAttachVolume and handleDetachVolume both call. Wrong
// mapping silently breaks AWS-SDK retry semantics: clients expect 4xx
// codes for caller-fixable problems and 5xx for server faults. A future
// edit that drops a sentinel branch would otherwise pass the mechanical
// "tests still compile" bar with no signal.
func TestAttachDetachErrorCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "ErrInstanceNotFound maps to InvalidInstanceID.NotFound",
			err:  vm.ErrInstanceNotFound,
			want: awserrors.ErrorInvalidInstanceIDNotFound,
		},
		{
			name: "wrapped ErrInstanceNotFound still matches via errors.Is",
			err:  fmt.Errorf("manager: %w", vm.ErrInstanceNotFound),
			want: awserrors.ErrorInvalidInstanceIDNotFound,
		},
		{
			name: "ErrInvalidTransition maps to IncorrectInstanceState",
			err:  vm.ErrInvalidTransition,
			want: awserrors.ErrorIncorrectInstanceState,
		},
		{
			name: "wrapped ErrInvalidTransition still matches",
			err:  fmt.Errorf("cannot attach in state stopped: %w", vm.ErrInvalidTransition),
			want: awserrors.ErrorIncorrectInstanceState,
		},
		{
			name: "ErrAttachmentLimitExceeded maps to AttachmentLimitExceeded",
			err:  vm.ErrAttachmentLimitExceeded,
			want: awserrors.ErrorAttachmentLimitExceeded,
		},
		{
			name: "ErrVolumeNotAttached maps to IncorrectState",
			err:  vm.ErrVolumeNotAttached,
			want: awserrors.ErrorIncorrectState,
		},
		{
			name: "wrapped ErrVolumeNotAttached still matches",
			err:  fmt.Errorf("%w: vol-1", vm.ErrVolumeNotAttached),
			want: awserrors.ErrorIncorrectState,
		},
		{
			name: "ErrVolumeNotDetachable maps to OperationNotPermitted",
			err:  vm.ErrVolumeNotDetachable,
			want: awserrors.ErrorOperationNotPermitted,
		},
		{
			name: "ErrVolumeDeviceMismatch maps to InvalidParameterValue",
			err:  vm.ErrVolumeDeviceMismatch,
			want: awserrors.ErrorInvalidParameterValue,
		},
		{
			name: "unknown error falls through to ServerInternal",
			err:  errors.New("QMP blockdev-add: connection refused"),
			want: awserrors.ErrorServerInternal,
		},
		{
			name: "wrapped unknown error falls through to ServerInternal",
			err:  fmt.Errorf("manager: %w", errors.New("nbdkit timeout")),
			want: awserrors.ErrorServerInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attachDetachErrorCode(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}
