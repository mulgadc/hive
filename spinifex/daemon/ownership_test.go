package daemon

import (
	"testing"

	"github.com/mulgadc/spinifex/spinifex/utils"
)

func TestIsInstanceVisible(t *testing.T) {
	tests := []struct {
		name            string
		callerAccountID string
		ownerAccountID  string
		expected        bool
	}{
		{
			name:            "same account",
			callerAccountID: "123456789012",
			ownerAccountID:  "123456789012",
			expected:        true,
		},
		{
			name:            "different account",
			callerAccountID: "123456789012",
			ownerAccountID:  "999999999999",
			expected:        false,
		},
		{
			name:            "pre-Phase4 instance visible to root",
			callerAccountID: utils.GlobalAccountID,
			ownerAccountID:  "",
			expected:        true,
		},
		{
			name:            "pre-Phase4 instance hidden from non-root",
			callerAccountID: "123456789012",
			ownerAccountID:  "",
			expected:        false,
		},
		{
			name:            "root accessing owned instance",
			callerAccountID: utils.GlobalAccountID,
			ownerAccountID:  utils.GlobalAccountID,
			expected:        true,
		},
		{
			name:            "root accessing other account instance",
			callerAccountID: utils.GlobalAccountID,
			ownerAccountID:  "123456789012",
			expected:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isInstanceVisible(tt.callerAccountID, tt.ownerAccountID)
			if got != tt.expected {
				t.Errorf("isInstanceVisible(%q, %q) = %v, want %v",
					tt.callerAccountID, tt.ownerAccountID, got, tt.expected)
			}
		})
	}
}
