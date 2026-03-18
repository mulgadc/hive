package instancetypes

import "slices"

// cpuGeneration represents a specific CPU microarchitecture generation
// and the AWS instance families it maps to.
type cpuGeneration struct {
	name     string   // e.g. "Intel Ice Lake", "AMD Genoa"
	families []string // e.g. ["t3", "c6i", "m6i", "r6i"]
}

var (
	// Intel generations
	genIntelBroadwell      = cpuGeneration{"Intel Broadwell", []string{"t2", "c4", "m4", "r4"}}
	genIntelSkylake        = cpuGeneration{"Intel Skylake/Cascade Lake", []string{"t3", "c5", "m5", "r5"}}
	genIntelIceLake        = cpuGeneration{"Intel Ice Lake", []string{"t3", "c6i", "m6i", "r6i"}}
	genIntelSapphireRapids = cpuGeneration{"Intel Sapphire Rapids", []string{"t3", "c7i", "m7i", "r7i"}}
	genIntelGraniteRapids  = cpuGeneration{"Intel Granite Rapids", []string{"t3", "c8i", "m8i", "r8i"}}

	// AMD generations
	genAMDZen  = cpuGeneration{"AMD Zen/Zen2 (Naples/Rome)", []string{"t3a", "c5a", "m5a", "r5a"}}
	genAMDZen3 = cpuGeneration{"AMD Zen 3 (Milan)", []string{"t3a", "c6a", "m6a", "r6a"}}
	genAMDZen4 = cpuGeneration{"AMD Zen 4 (Genoa)", []string{"t3a", "c7a", "m7a", "r7a"}}
	genAMDZen5 = cpuGeneration{"AMD Zen 5 (Turin)", []string{"t3a", "c8a", "m8a", "r8a"}}

	// ARM generations
	genARMNeoverseN1 = cpuGeneration{"ARM Neoverse N1 (Graviton2)", []string{"t4g", "c6g", "m6g", "r6g"}}
	genARMNeoverseV1 = cpuGeneration{"ARM Neoverse V1 (Graviton3)", []string{"t4g", "c7g", "m7g", "r7g"}}
	genARMNeoverseV2 = cpuGeneration{"ARM Neoverse V2 (Graviton4)", []string{"t4g", "c8g", "m8g", "r8g"}}

	// Unknown/fallback — expose only burstable family
	genUnknownIntel = cpuGeneration{"Unknown Intel", []string{"t3"}}
	genUnknownAMD   = cpuGeneration{"Unknown AMD", []string{"t3a"}}
	genUnknownARM   = cpuGeneration{"Unknown ARM", []string{"t4g"}}
	genUnknown      = cpuGeneration{"Unknown", []string{"t3"}}
)

type instanceSize struct {
	suffix   string
	vcpus    int
	memoryGB float64
}

type instanceFamilyDef struct {
	name       string
	sizes      []instanceSize
	currentGen bool
}

// Size tables for each instance category

var burstableSizes = []instanceSize{
	{"nano", 2, 0.5},
	{"micro", 2, 1},
	{"small", 2, 2},
	{"medium", 2, 4},
	{"large", 2, 8},
	{"xlarge", 4, 16},
	{"2xlarge", 8, 32},
}

var gpSizes = []instanceSize{
	{"large", 2, 8},
	{"xlarge", 4, 16},
	{"2xlarge", 8, 32},
	{"4xlarge", 16, 64},
	{"8xlarge", 32, 128},
	{"12xlarge", 48, 192},
	{"16xlarge", 64, 256},
	{"24xlarge", 96, 384},
}

// gpSizesSmall is gpSizes without 12xlarge and 24xlarge (older/ARM families).
var gpSizesSmall = slices.Clone(gpSizes[:6])

var computeSizes = []instanceSize{
	{"large", 2, 4},
	{"xlarge", 4, 8},
	{"2xlarge", 8, 16},
	{"4xlarge", 16, 32},
	{"8xlarge", 32, 64},
	{"12xlarge", 48, 96},
	{"16xlarge", 64, 128},
	{"24xlarge", 96, 192},
}

// computeSizesSmall is computeSizes without 12xlarge and 24xlarge (older/ARM families).
var computeSizesSmall = slices.Clone(computeSizes[:6])

var memorySizes = []instanceSize{
	{"large", 2, 16},
	{"xlarge", 4, 32},
	{"2xlarge", 8, 64},
	{"4xlarge", 16, 128},
	{"8xlarge", 32, 256},
	{"12xlarge", 48, 384},
	{"16xlarge", 64, 512},
	{"24xlarge", 96, 768},
}

// memorySizesSmall is memorySizes without 12xlarge and 24xlarge (older/ARM families).
var memorySizesSmall = slices.Clone(memorySizes[:6])

// instanceFamilyDefs defines all supported instance families with their vendor and sizes.
//
// We support the core families across burstable, general purpose, compute optimized,
// and memory optimized categories. The following AWS family categories are intentionally
// excluded because they require specialized hardware not available on standard bare-metal hosts:
//
//   - Local disk variants (d/n suffixes): c5d, c5ad, c5n, m5d, m5ad, m5n, m5dn, m5zn, r5d, r5ad,
//     r5n, r5dn, r5b, c6gd, c6gn, c6id, c6in, m6gd, m6id, m6idn, m6in, r6gd, r6id, r6idn, r6in,
//     c7gd, c7gn, c7i-flex, m7gd, m7i-flex, r7gd, r7iz, c8gd, c8gn, c8i-flex, m8gd, m8i-flex,
//     r8gd, r8gn, r8gb, r8i-flex — require NVMe instance storage or enhanced networking
//   - GPU/accelerator: g2-g6, g6e, g6f, gr6, gr6f, p2-p6, inf1-inf2, trn1-trn2, dl1, dl2q — require
//     GPU, Inferentia, Trainium, or other accelerator hardware. note inf1 and trn1 are not supported since its aws only hardware.
//   - Storage optimized: d2, d3, d3en, h1, i2-i8g, i7ie, i8ge, im4gn, is4gen — require dense HDD/NVMe
//   - FPGA: f1, f2 — require FPGA hardware
//   - High memory: u-*, u7i-*, x1, x1e, x2gd, x2idn, x2iedn, x2iezn, x8g — require TB-scale memory
//   - High frequency: z1d — specialized high clock-speed instances
//   - (unsupported) Dedicated host: mac*, hpc* — require macOS/Apple hardware or HPC interconnects
//   - (unsupported) Video: vt1 — requires video transcoding hardware
//   - Legacy (pre-gen4): a1, c1, c3, cc1, cc2, cg1, cr1, hi1, hs1, m1, m2, m3, r3, t1
var instanceFamilyDefs = []instanceFamilyDef{
	// Burstable
	{name: "t2", sizes: burstableSizes, currentGen: false},
	{name: "t3", sizes: burstableSizes, currentGen: true},
	{name: "t3a", sizes: burstableSizes, currentGen: true},
	{name: "t4g", sizes: burstableSizes, currentGen: true},

	// General Purpose (1:4 vCPU:memory)
	{name: "m4", sizes: gpSizesSmall, currentGen: false},
	{name: "m5", sizes: gpSizes, currentGen: true},
	{name: "m5a", sizes: gpSizes, currentGen: true},
	{name: "m6i", sizes: gpSizes, currentGen: true},
	{name: "m6a", sizes: gpSizes, currentGen: true},
	{name: "m6g", sizes: gpSizesSmall, currentGen: true},
	{name: "m7i", sizes: gpSizes, currentGen: true},
	{name: "m7a", sizes: gpSizes, currentGen: true},
	{name: "m7g", sizes: gpSizesSmall, currentGen: true},
	{name: "m8i", sizes: gpSizes, currentGen: true},
	{name: "m8a", sizes: gpSizes, currentGen: true},
	{name: "m8g", sizes: gpSizesSmall, currentGen: true},

	// Compute Optimized (1:2 vCPU:memory)
	{name: "c4", sizes: computeSizesSmall, currentGen: false},
	{name: "c5", sizes: computeSizes, currentGen: true},
	{name: "c5a", sizes: computeSizes, currentGen: true},
	{name: "c6i", sizes: computeSizes, currentGen: true},
	{name: "c6a", sizes: computeSizes, currentGen: true},
	{name: "c6g", sizes: computeSizesSmall, currentGen: true},
	{name: "c7i", sizes: computeSizes, currentGen: true},
	{name: "c7a", sizes: computeSizes, currentGen: true},
	{name: "c7g", sizes: computeSizesSmall, currentGen: true},
	{name: "c8i", sizes: computeSizes, currentGen: true},
	{name: "c8a", sizes: computeSizes, currentGen: true},
	{name: "c8g", sizes: computeSizesSmall, currentGen: true},

	// Memory Optimized (1:8 vCPU:memory)
	{name: "r4", sizes: memorySizesSmall, currentGen: false},
	{name: "r5", sizes: memorySizes, currentGen: true},
	{name: "r5a", sizes: memorySizes, currentGen: true},
	{name: "r6i", sizes: memorySizes, currentGen: true},
	{name: "r6a", sizes: memorySizes, currentGen: true},
	{name: "r6g", sizes: memorySizesSmall, currentGen: true},
	{name: "r7i", sizes: memorySizes, currentGen: true},
	{name: "r7a", sizes: memorySizes, currentGen: true},
	{name: "r7g", sizes: memorySizesSmall, currentGen: true},
	{name: "r8i", sizes: memorySizes, currentGen: true},
	{name: "r8a", sizes: memorySizes, currentGen: true},
	{name: "r8g", sizes: memorySizesSmall, currentGen: true},
}
