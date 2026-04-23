package daemon

import (
	"errors"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/spinifex/spinifex/types"
)

// errInsufficientCapacity is returned by allocateForLaunch when MinCount
// cannot be satisfied.
var errInsufficientCapacity = errors.New("insufficient capacity to satisfy MinCount")

// canAllocateCount returns how many instances of the given type can fit
// in the remaining capacity, capped at maxCount. Pure function — no locks
// or side effects.
//
// availGPU is the number of free GPUs in the pool; requiresGPU indicates
// that this instance type needs one. When requiresGPU is true and availGPU
// is zero, the result is always 0.
func canAllocateCount(availVCPU, allocVCPU int, availMem, allocMem float64,
	vCPUs int64, memMiB int64, maxCount int,
	availGPU int, requiresGPU bool) int {
	if requiresGPU && availGPU == 0 {
		return 0
	}

	remainingVCPU := availVCPU - allocVCPU
	remainingMem := availMem - allocMem
	memoryGB := float64(memMiB) / 1024.0

	countByCPU := maxCount
	if vCPUs > 0 {
		countByCPU = remainingVCPU / int(vCPUs)
	}

	countByMem := maxCount
	if memoryGB > 0 {
		countByMem = int(remainingMem / memoryGB)
	}

	result := min(countByMem, countByCPU)
	if requiresGPU {
		result = min(result, availGPU)
	}
	result = min(result, maxCount)
	return max(result, 0)
}

// resourceStatsForType computes the InstanceTypeCap for a single instance type
// given the remaining host resources. Pure function — no locks or side effects.
func resourceStatsForType(remainVCPU int, remainMem float64, it *ec2.InstanceTypeInfo) types.InstanceTypeCap {
	vCPUs := instanceTypeVCPUs(it)
	memGB := float64(instanceTypeMemoryMiB(it)) / 1024.0

	count := 0
	if vCPUs > 0 && memGB > 0 {
		countVCPU := remainVCPU / int(vCPUs)
		countMem := int(remainMem / memGB)
		count = max(min(countMem, countVCPU), 0)
	}

	name := ""
	if it.InstanceType != nil {
		name = *it.InstanceType
	}

	return types.InstanceTypeCap{
		Name:      name,
		VCPU:      int(vCPUs),
		MemoryGB:  memGB,
		Available: count,
	}
}

// allocateForLaunch determines the number of instances to launch given
// available capacity and the MinCount/MaxCount constraints from a
// RunInstances request. Returns the launch count or an error if the
// minimum cannot be satisfied.
func allocateForLaunch(canAlloc, minCount, maxCount int) (int, error) {
	if canAlloc < minCount {
		return 0, errInsufficientCapacity
	}
	return min(canAlloc, maxCount), nil
}
