package utils

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Each wrapper has one or two clamp branches; tests cover the no-branch
// happy path and each branch. Boundary tests against MaxInt64/MaxUint8 etc.
// only exercise stdlib casts, so they are intentionally omitted.

func TestSafeInt64ToUint64(t *testing.T) {
	tests := []struct {
		name string
		in   int64
		want uint64
	}{
		{"positive passes through", 42, 42},
		{"negative clamps to 0", -1, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SafeInt64ToUint64(tt.in))
		})
	}
}

func TestSafeIntToUint8(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want uint8
	}{
		{"in range passes through", 128, 128},
		{"above max clamps", math.MaxUint8 + 1, math.MaxUint8},
		{"negative clamps to 0", -1, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SafeIntToUint8(tt.in))
		})
	}
}

func TestSafeIntToUint64(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want uint64
	}{
		{"positive passes through", 42, 42},
		{"negative clamps to 0", -1, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SafeIntToUint64(tt.in))
		})
	}
}

func TestSafeUint64ToInt64(t *testing.T) {
	tests := []struct {
		name string
		in   uint64
		want int64
	}{
		{"in range passes through", 42, 42},
		{"above MaxInt64 clamps", uint64(math.MaxInt64) + 1, math.MaxInt64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SafeUint64ToInt64(tt.in))
		})
	}
}
