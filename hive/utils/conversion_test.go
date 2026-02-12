package utils

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSafeInt64ToUint64(t *testing.T) {
	tests := []struct {
		name string
		in   int64
		want uint64
	}{
		{"Zero", 0, 0},
		{"Positive", 42, 42},
		{"MaxInt64", math.MaxInt64, uint64(math.MaxInt64)},
		{"Negative", -1, 0},
		{"MinInt64", math.MinInt64, 0},
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
		{"Zero", 0, 0},
		{"Normal", 128, 128},
		{"Max uint8", 255, 255},
		{"Above max", 256, 255},
		{"Large", 1000, 255},
		{"Negative", -1, 0},
		{"Large negative", -100, 0},
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
		{"Zero", 0, 0},
		{"Positive", 42, 42},
		{"Negative", -1, 0},
		{"Large negative", -999, 0},
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
		{"Zero", 0, 0},
		{"Normal", 42, 42},
		{"MaxInt64", uint64(math.MaxInt64), math.MaxInt64},
		{"Above MaxInt64", uint64(math.MaxInt64) + 1, math.MaxInt64},
		{"MaxUint64", math.MaxUint64, math.MaxInt64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SafeUint64ToInt64(tt.in))
		})
	}
}
