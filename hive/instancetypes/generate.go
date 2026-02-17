package instancetypes

import (
	"fmt"
	"log/slog"
	"runtime"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

// generateForGeneration creates the instance type map for the given CPU generation.
// It generates all instance families matching the generation's family list across
// burstable, general purpose, compute optimized, and memory optimized categories.
func generateForGeneration(gen cpuGeneration, arch string) map[string]*ec2.InstanceTypeInfo {
	// Build a set of allowed families for fast lookup
	allowed := make(map[string]bool, len(gen.families))
	for _, f := range gen.families {
		allowed[f] = true
	}

	instanceTypes := make(map[string]*ec2.InstanceTypeInfo)
	for _, def := range instanceFamilyDefs {
		if !allowed[def.name] {
			continue
		}
		burstable := strings.HasPrefix(def.name, "t")
		for _, size := range def.sizes {
			name := fmt.Sprintf("%s.%s", def.name, size.suffix)
			instanceTypes[name] = &ec2.InstanceTypeInfo{
				InstanceType: aws.String(name),
				VCpuInfo: &ec2.VCpuInfo{
					DefaultVCpus: aws.Int64(int64(size.vcpus)),
				},
				MemoryInfo: &ec2.MemoryInfo{
					SizeInMiB: aws.Int64(int64(size.memoryGB * 1024)),
				},
				ProcessorInfo: &ec2.ProcessorInfo{
					SupportedArchitectures: []*string{aws.String(arch)},
				},
				CurrentGeneration:             aws.Bool(def.currentGen),
				BurstablePerformanceSupported: aws.Bool(burstable),
				Hypervisor:                    aws.String("kvm"),
				SupportedVirtualizationTypes:  []*string{aws.String("hvm")},
				SupportedRootDeviceTypes:      []*string{aws.String("ebs")},
			}
		}
	}
	return instanceTypes
}

// DetectAndGenerate detects the host CPU generation and generates matching instance types.
func DetectAndGenerate(cpu CPUInfo, arch string) map[string]*ec2.InstanceTypeInfo {
	gen := detectCPUGeneration(cpu, arch)
	types := generateForGeneration(gen, arch)

	if len(types) == 0 {
		slog.Error("No instance types generated, daemon will be unable to run VMs",
			"generation", gen.name, "arch", arch)
	} else {
		slog.Info("CPU generation detected",
			"generation", gen.name, "families", gen.families,
			"instanceTypes", len(types), "os", runtime.GOOS)
	}

	return types
}
