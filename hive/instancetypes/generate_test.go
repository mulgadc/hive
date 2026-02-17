package instancetypes

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hasFamily(types map[string]*ec2.InstanceTypeInfo, prefix string) bool {
	for name := range types {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func countFamily(types map[string]*ec2.InstanceTypeInfo, prefix string) int {
	count := 0
	for name := range types {
		if strings.HasPrefix(name, prefix) {
			count++
		}
	}
	return count
}

func TestGenerateInstanceTypes_IntelIceLake(t *testing.T) {
	types := GenerateForGeneration(genIntelIceLake, "x86_64")
	// t3(7) + c6i(8) + m6i(8) + r6i(8) = 31
	assert.Len(t, types, 31)

	for _, prefix := range []string{"t3.", "c6i.", "m6i.", "r6i."} {
		assert.True(t, hasFamily(types, prefix), "expected Ice Lake types to include %s family", prefix)
	}

	// Verify other generation families are NOT present
	for name := range types {
		assert.False(t, strings.HasPrefix(name, "t3a."), "Ice Lake should not have t3a: %s", name)
		assert.False(t, strings.HasPrefix(name, "c5."), "Ice Lake should not have c5: %s", name)
		assert.False(t, strings.HasPrefix(name, "c7i."), "Ice Lake should not have c7i: %s", name)
	}
}

func TestGenerateInstanceTypes_IntelBroadwell(t *testing.T) {
	types := GenerateForGeneration(genIntelBroadwell, "x86_64")
	// t2(7) + c4(6) + m4(6) + r4(6) = 25
	assert.Len(t, types, 25)

	for _, prefix := range []string{"t2.", "c4.", "m4.", "r4."} {
		assert.True(t, hasFamily(types, prefix), "expected Broadwell types to include %s family", prefix)
	}
}

func TestGenerateInstanceTypes_IntelSkylake(t *testing.T) {
	types := GenerateForGeneration(genIntelSkylake, "x86_64")
	// t3(7) + c5(8) + m5(8) + r5(8) = 31
	assert.Len(t, types, 31)

	for _, prefix := range []string{"t3.", "c5.", "m5.", "r5."} {
		assert.True(t, hasFamily(types, prefix), "expected Skylake types to include %s family", prefix)
	}
}

func TestGenerateInstanceTypes_IntelSapphireRapids(t *testing.T) {
	types := GenerateForGeneration(genIntelSapphireRapids, "x86_64")
	// t3(7) + c7i(8) + m7i(8) + r7i(8) = 31
	assert.Len(t, types, 31)

	for _, prefix := range []string{"t3.", "c7i.", "m7i.", "r7i."} {
		assert.True(t, hasFamily(types, prefix), "expected Sapphire Rapids types to include %s family", prefix)
	}
}

func TestGenerateInstanceTypes_IntelGraniteRapids(t *testing.T) {
	types := GenerateForGeneration(genIntelGraniteRapids, "x86_64")
	// t3(7) + c8i(8) + m8i(8) + r8i(8) = 31
	assert.Len(t, types, 31)

	for _, prefix := range []string{"t3.", "c8i.", "m8i.", "r8i."} {
		assert.True(t, hasFamily(types, prefix), "expected Granite Rapids types to include %s family", prefix)
	}
}

func TestGenerateInstanceTypes_AMDZen(t *testing.T) {
	types := GenerateForGeneration(genAMDZen, "x86_64")
	// t3a(7) + c5a(8) + m5a(8) + r5a(8) = 31
	assert.Len(t, types, 31)

	for _, prefix := range []string{"t3a.", "c5a.", "m5a.", "r5a."} {
		assert.True(t, hasFamily(types, prefix), "expected Zen types to include %s family", prefix)
	}
}

func TestGenerateInstanceTypes_AMDZen4(t *testing.T) {
	types := GenerateForGeneration(genAMDZen4, "x86_64")
	// t3a(7) + c7a(8) + m7a(8) + r7a(8) = 31
	assert.Len(t, types, 31)

	for _, prefix := range []string{"t3a.", "c7a.", "m7a.", "r7a."} {
		assert.True(t, hasFamily(types, prefix), "expected Zen 4 types to include %s family", prefix)
	}

	// Verify older AMD families are NOT present
	for name := range types {
		assert.False(t, strings.HasPrefix(name, "c5a."), "Zen4 should not have c5a: %s", name)
		assert.False(t, strings.HasPrefix(name, "c6a."), "Zen4 should not have c6a: %s", name)
	}
}

func TestGenerateInstanceTypes_ARMN1(t *testing.T) {
	types := GenerateForGeneration(genARMNeoverseN1, "arm64")
	// t4g(7) + c6g(6) + m6g(6) + r6g(6) = 25
	assert.Len(t, types, 25)

	for _, prefix := range []string{"t4g.", "c6g.", "m6g.", "r6g."} {
		assert.True(t, hasFamily(types, prefix), "expected N1 types to include %s family", prefix)
	}

	// Verify Intel/AMD families are NOT present
	for name := range types {
		assert.False(t, strings.HasPrefix(name, "t3."), "ARM should not have t3: %s", name)
		assert.False(t, strings.HasPrefix(name, "t3a."), "ARM should not have t3a: %s", name)
	}
}

func TestGenerateInstanceTypes_ARMV2(t *testing.T) {
	types := GenerateForGeneration(genARMNeoverseV2, "arm64")
	// t4g(7) + c8g(6) + m8g(6) + r8g(6) = 25
	assert.Len(t, types, 25)

	for _, prefix := range []string{"t4g.", "c8g.", "m8g.", "r8g."} {
		assert.True(t, hasFamily(types, prefix), "expected V2 types to include %s family", prefix)
	}
}

func TestGenerateInstanceTypes_UnknownFallback(t *testing.T) {
	types := GenerateForGeneration(genUnknownIntel, "x86_64")
	// Unknown Intel: t3 only = 7 types
	assert.Len(t, types, 7)
	assert.True(t, hasFamily(types, "t3."), "unknown Intel should have t3")

	types = GenerateForGeneration(genUnknownAMD, "x86_64")
	assert.Len(t, types, 7)
	assert.True(t, hasFamily(types, "t3a."), "unknown AMD should have t3a")

	types = GenerateForGeneration(genUnknownARM, "arm64")
	assert.Len(t, types, 7)
	assert.True(t, hasFamily(types, "t4g."), "unknown ARM should have t4g")

	types = GenerateForGeneration(genUnknown, "x86_64")
	assert.Len(t, types, 7)
	assert.True(t, hasFamily(types, "t3."), "completely unknown should have t3")
}

func TestGenerateInstanceTypes_VerifyBurstableSizes(t *testing.T) {
	types := GenerateForGeneration(genIntelSkylake, "x86_64")

	expected := map[string]struct {
		vcpus int64
		memMB int64
	}{
		"t3.nano":    {2, 512},
		"t3.micro":   {2, 1024},
		"t3.small":   {2, 2048},
		"t3.medium":  {2, 4096},
		"t3.large":   {2, 8192},
		"t3.xlarge":  {4, 16384},
		"t3.2xlarge": {8, 32768},
	}

	for name, exp := range expected {
		it, ok := types[name]
		require.True(t, ok, "missing instance type %s", name)
		assert.Equal(t, exp.vcpus, *it.VCpuInfo.DefaultVCpus, "%s vCPUs", name)
		assert.Equal(t, exp.memMB, *it.MemoryInfo.SizeInMiB, "%s memory", name)
	}
}

func TestGenerateInstanceTypes_ComputeRatio(t *testing.T) {
	// Skylake for c5
	skylakeTypes := GenerateForGeneration(genIntelSkylake, "x86_64")
	expectedSkylake := map[string]struct {
		vcpus int64
		memMB int64
	}{
		"c5.large":   {2, 4096},
		"c5.xlarge":  {4, 8192},
		"c5.2xlarge": {8, 16384},
	}

	for name, exp := range expectedSkylake {
		it, ok := skylakeTypes[name]
		require.True(t, ok, "missing instance type %s", name)
		assert.Equal(t, exp.vcpus, *it.VCpuInfo.DefaultVCpus, "%s vCPUs", name)
		assert.Equal(t, exp.memMB, *it.MemoryInfo.SizeInMiB, "%s memory", name)
	}

	// Sapphire Rapids for c7i
	sapphireTypes := GenerateForGeneration(genIntelSapphireRapids, "x86_64")
	it, ok := sapphireTypes["c7i.4xlarge"]
	require.True(t, ok, "missing instance type c7i.4xlarge")
	assert.Equal(t, int64(16), *it.VCpuInfo.DefaultVCpus, "c7i.4xlarge vCPUs")
	assert.Equal(t, int64(32768), *it.MemoryInfo.SizeInMiB, "c7i.4xlarge memory")
}

func TestGenerateInstanceTypes_MemoryRatio(t *testing.T) {
	// Skylake for r5
	skylakeTypes := GenerateForGeneration(genIntelSkylake, "x86_64")
	expectedSkylake := map[string]struct {
		vcpus int64
		memMB int64
	}{
		"r5.large":   {2, 16384},
		"r5.xlarge":  {4, 32768},
		"r5.2xlarge": {8, 65536},
	}

	for name, exp := range expectedSkylake {
		it, ok := skylakeTypes[name]
		require.True(t, ok, "missing instance type %s", name)
		assert.Equal(t, exp.vcpus, *it.VCpuInfo.DefaultVCpus, "%s vCPUs", name)
		assert.Equal(t, exp.memMB, *it.MemoryInfo.SizeInMiB, "%s memory", name)
	}

	// Sapphire Rapids for r7i
	sapphireTypes := GenerateForGeneration(genIntelSapphireRapids, "x86_64")
	it, ok := sapphireTypes["r7i.4xlarge"]
	require.True(t, ok, "missing instance type r7i.4xlarge")
	assert.Equal(t, int64(16), *it.VCpuInfo.DefaultVCpus, "r7i.4xlarge vCPUs")
	assert.Equal(t, int64(131072), *it.MemoryInfo.SizeInMiB, "r7i.4xlarge memory")
}

func TestGenerateInstanceTypes_NoSmallSizesForNonBurstable(t *testing.T) {
	types := GenerateForGeneration(genIntelSkylake, "x86_64")

	// Non-burstable families should not have nano/micro/small/medium sizes
	for name := range types {
		if strings.HasPrefix(name, "t") {
			continue // skip all burstable families
		}
		for _, small := range []string{".nano", ".micro", ".small", ".medium"} {
			assert.False(t, strings.HasSuffix(name, small),
				"non-burstable type %s should not have %s size", name, small)
		}
	}
}

func TestGenerateInstanceTypes_OlderFamiliesHaveSmallerSizeRange(t *testing.T) {
	// Broadwell has m4 = 6 sizes
	broadwellTypes := GenerateForGeneration(genIntelBroadwell, "x86_64")
	assert.Equal(t, 6, countFamily(broadwellTypes, "m4."), "m4 should have 6 sizes (large → 16xlarge)")

	// Skylake has m5 = 8 sizes
	skylakeTypes := GenerateForGeneration(genIntelSkylake, "x86_64")
	assert.Equal(t, 8, countFamily(skylakeTypes, "m5."), "m5 should have 8 sizes (large → 24xlarge)")
}

func TestGenerateInstanceTypes_BurstableFlag(t *testing.T) {
	// Test Broadwell (has prev-gen families)
	broadwellTypes := GenerateForGeneration(genIntelBroadwell, "x86_64")
	prevGen := map[string]bool{"t2": true, "m4": true, "c4": true, "r4": true}

	for name, info := range broadwellTypes {
		isBurstable := strings.HasPrefix(name, "t")
		family := strings.SplitN(name, ".", 2)[0]
		assert.Equal(t, isBurstable, *info.BurstablePerformanceSupported,
			"%s burstable flag mismatch", name)
		assert.Equal(t, !prevGen[family], *info.CurrentGeneration,
			"%s current generation flag mismatch", name)
	}

	// Test current-gen (Sapphire Rapids) — all families should be currentGen=true
	sapphireTypes := GenerateForGeneration(genIntelSapphireRapids, "x86_64")
	for name, info := range sapphireTypes {
		isBurstable := strings.HasPrefix(name, "t")
		assert.Equal(t, isBurstable, *info.BurstablePerformanceSupported,
			"%s burstable flag mismatch", name)
		assert.True(t, *info.CurrentGeneration,
			"%s should be current generation", name)
	}
}
