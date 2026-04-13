package handlers_ec2_vpc

import (
	"sync"
	"testing"

	"github.com/mulgadc/spinifex/spinifex/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestExternalIPAM(t *testing.T, pools []ExternalPoolConfig) *ExternalIPAM {
	t.Helper()
	_, _, js := testutil.StartTestJetStream(t)

	ipam, err := NewExternalIPAM(js, pools)
	require.NoError(t, err)
	return ipam
}

func testPool() ExternalPoolConfig {
	return ExternalPoolConfig{
		Name:       "wan",
		RangeStart: "192.168.1.150",
		RangeEnd:   "192.168.1.160",
		Gateway:    "192.168.1.1",
		PrefixLen:  24,
	}
}

func TestExternalIPAM_AllocateSequential(t *testing.T) {
	pool := testPool()
	ipam := setupTestExternalIPAM(t, []ExternalPoolConfig{pool})

	// .150 is reserved for gateway, so first allocable is .151
	ip1, poolName, err := ipam.AllocateIP("", "", "auto_assign", "", "eni-1", "i-1")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.151", ip1)
	assert.Equal(t, "wan", poolName)

	ip2, _, err := ipam.AllocateIP("", "", "auto_assign", "", "eni-2", "i-2")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.152", ip2)

	ip3, _, err := ipam.AllocateIP("", "", "auto_assign", "", "eni-3", "i-3")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.153", ip3)
}

func TestExternalIPAM_GatewayIPReserved(t *testing.T) {
	pool := testPool()
	ipam := setupTestExternalIPAM(t, []ExternalPoolConfig{pool})

	record, err := ipam.GetPoolRecord("wan")
	require.NoError(t, err)

	// Gateway IP (.150) should be reserved
	alloc, ok := record.Allocated["192.168.1.150"]
	assert.True(t, ok, "gateway IP should be in allocated map")
	assert.Equal(t, "gateway", alloc.Type)

	// Cannot release gateway IP
	err = ipam.ReleaseIP("wan", "192.168.1.150")
	assert.ErrorContains(t, err, "cannot release gateway IP")
}

func TestExternalIPAM_ExplicitGatewayIP(t *testing.T) {
	pool := testPool()
	pool.GatewayIP = "192.168.1.155"
	ipam := setupTestExternalIPAM(t, []ExternalPoolConfig{pool})

	record, err := ipam.GetPoolRecord("wan")
	require.NoError(t, err)

	// Explicit gateway IP (.155) reserved, not .150
	_, ok := record.Allocated["192.168.1.155"]
	assert.True(t, ok)
	_, ok = record.Allocated["192.168.1.150"]
	assert.False(t, ok)

	// First allocable is .150 since .155 is the gateway
	ip1, _, err := ipam.AllocateIP("", "", "auto_assign", "", "eni-1", "i-1")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.150", ip1)
}

func TestExternalIPAM_Release(t *testing.T) {
	pool := testPool()
	ipam := setupTestExternalIPAM(t, []ExternalPoolConfig{pool})

	ip1, _, err := ipam.AllocateIP("", "", "auto_assign", "", "eni-1", "i-1")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.151", ip1)

	ip2, _, err := ipam.AllocateIP("", "", "auto_assign", "", "eni-2", "i-2")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.152", ip2)

	// Release first
	err = ipam.ReleaseIP("wan", "192.168.1.151")
	require.NoError(t, err)

	// Next allocation reuses released IP
	ip3, _, err := ipam.AllocateIP("", "", "auto_assign", "", "eni-3", "i-3")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.151", ip3)
}

func TestExternalIPAM_Exhaustion(t *testing.T) {
	pool := ExternalPoolConfig{
		Name:       "tiny",
		RangeStart: "10.0.0.1",
		RangeEnd:   "10.0.0.3",
		Gateway:    "10.0.0.254",
		PrefixLen:  24,
	}
	ipam := setupTestExternalIPAM(t, []ExternalPoolConfig{pool})

	// .1 reserved for gateway, .2 and .3 allocable
	ip1, _, err := ipam.AllocateIP("", "", "auto_assign", "", "eni-1", "i-1")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.2", ip1)

	ip2, _, err := ipam.AllocateIP("", "", "auto_assign", "", "eni-2", "i-2")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.3", ip2)

	// Pool exhausted
	_, _, err = ipam.AllocateIP("", "", "auto_assign", "", "eni-3", "i-3")
	assert.ErrorContains(t, err, "InsufficientAddressCapacity")
}

func TestExternalIPAM_CASConflict(t *testing.T) {
	pool := testPool()
	ipam := setupTestExternalIPAM(t, []ExternalPoolConfig{pool})

	// Concurrent allocations should all succeed (CAS retry handles conflicts)
	var wg sync.WaitGroup
	results := make([]string, 5)
	errs := make([]error, 5)

	for i := range 5 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ip, _, err := ipam.AllocateIP("", "", "auto_assign", "", "eni-"+itoa(idx), "i-"+itoa(idx))
			results[idx] = ip
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	// All should succeed
	for i, err := range errs {
		assert.NoError(t, err, "concurrent allocation %d should succeed", i)
	}

	// All IPs should be unique
	seen := make(map[string]bool)
	for _, ip := range results {
		assert.False(t, seen[ip], "duplicate IP: %s", ip)
		seen[ip] = true
	}
}

func TestExternalIPAM_MultiPool(t *testing.T) {
	pools := []ExternalPoolConfig{
		{
			Name:       "us-east",
			RangeStart: "203.0.113.2",
			RangeEnd:   "203.0.113.10",
			Gateway:    "203.0.113.1",
			PrefixLen:  24,
			Region:     "us-east-1",
			AZ:         "us-east-1a",
		},
		{
			Name:       "eu-west",
			RangeStart: "198.51.100.2",
			RangeEnd:   "198.51.100.10",
			Gateway:    "198.51.100.1",
			PrefixLen:  24,
			Region:     "eu-west-1",
			AZ:         "eu-west-1a",
		},
	}
	ipam := setupTestExternalIPAM(t, pools)

	// Allocate from US pool
	ip1, poolName1, err := ipam.AllocateIP("us-east-1", "us-east-1a", "auto_assign", "", "eni-1", "i-1")
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.3", ip1) // .2 reserved for gateway
	assert.Equal(t, "us-east", poolName1)

	// Allocate from EU pool
	ip2, poolName2, err := ipam.AllocateIP("eu-west-1", "eu-west-1a", "auto_assign", "", "eni-2", "i-2")
	require.NoError(t, err)
	assert.Equal(t, "198.51.100.3", ip2)
	assert.Equal(t, "eu-west", poolName2)
}

func TestExternalIPAM_PoolFallback(t *testing.T) {
	pools := []ExternalPoolConfig{
		{
			Name:       "az-pool",
			RangeStart: "10.0.0.1",
			RangeEnd:   "10.0.0.2",
			Gateway:    "10.0.0.254",
			PrefixLen:  24,
			Region:     "us-east-1",
			AZ:         "us-east-1a",
		},
		{
			Name:       "region-pool",
			RangeStart: "10.0.1.1",
			RangeEnd:   "10.0.1.10",
			Gateway:    "10.0.1.254",
			PrefixLen:  24,
			Region:     "us-east-1",
		},
		{
			Name:       "global-pool",
			RangeStart: "10.0.2.1",
			RangeEnd:   "10.0.2.10",
			Gateway:    "10.0.2.254",
			PrefixLen:  24,
		},
	}
	ipam := setupTestExternalIPAM(t, pools)

	// AZ match: us-east-1a → az-pool
	ip1, pool1, err := ipam.AllocateIP("us-east-1", "us-east-1a", "auto_assign", "", "eni-1", "i-1")
	require.NoError(t, err)
	assert.Equal(t, "az-pool", pool1)
	assert.Equal(t, "10.0.0.2", ip1) // .1 reserved for gw

	// Region match (different AZ): us-east-1b → region-pool
	ip2, pool2, err := ipam.AllocateIP("us-east-1", "us-east-1b", "auto_assign", "", "eni-2", "i-2")
	require.NoError(t, err)
	assert.Equal(t, "region-pool", pool2)
	assert.Equal(t, "10.0.1.2", ip2)

	// No match: eu-west-1 → global-pool
	ip3, pool3, err := ipam.AllocateIP("eu-west-1", "eu-west-1a", "auto_assign", "", "eni-3", "i-3")
	require.NoError(t, err)
	assert.Equal(t, "global-pool", pool3)
	assert.Equal(t, "10.0.2.2", ip3)
}

func TestExternalIPAM_SpecificPool(t *testing.T) {
	pools := []ExternalPoolConfig{
		{
			Name:       "pool-a",
			RangeStart: "10.0.0.1",
			RangeEnd:   "10.0.0.10",
			Gateway:    "10.0.0.254",
			PrefixLen:  24,
		},
		{
			Name:       "pool-b",
			RangeStart: "10.0.1.1",
			RangeEnd:   "10.0.1.10",
			Gateway:    "10.0.1.254",
			PrefixLen:  24,
		},
	}
	ipam := setupTestExternalIPAM(t, pools)

	// Allocate specifically from pool-b
	ip, err := ipam.AllocateFromPool("pool-b", "elastic_ip", "eipalloc-test-b", "", "")
	require.NoError(t, err)
	assert.Equal(t, "10.0.1.2", ip) // .1 reserved for gw
}

func TestExternalIPAM_RangeValidation(t *testing.T) {
	tests := []struct {
		name    string
		pool    ExternalPoolConfig
		wantErr string
	}{
		{
			name:    "empty name",
			pool:    ExternalPoolConfig{RangeStart: "10.0.0.1", RangeEnd: "10.0.0.10", Gateway: "10.0.0.254"},
			wantErr: "pool name is required",
		},
		{
			name:    "bad range_start",
			pool:    ExternalPoolConfig{Name: "bad", RangeStart: "not-an-ip", RangeEnd: "10.0.0.10", Gateway: "10.0.0.254"},
			wantErr: "invalid range_start",
		},
		{
			name:    "bad range_end",
			pool:    ExternalPoolConfig{Name: "bad", RangeStart: "10.0.0.1", RangeEnd: "not-an-ip", Gateway: "10.0.0.254"},
			wantErr: "invalid range_end",
		},
		{
			name:    "start > end",
			pool:    ExternalPoolConfig{Name: "bad", RangeStart: "10.0.0.10", RangeEnd: "10.0.0.1", Gateway: "10.0.0.254"},
			wantErr: "greater than range_end",
		},
		{
			name:    "missing gateway",
			pool:    ExternalPoolConfig{Name: "bad", RangeStart: "10.0.0.1", RangeEnd: "10.0.0.10"},
			wantErr: "gateway is required",
		},
		{
			name:    "bad gateway",
			pool:    ExternalPoolConfig{Name: "bad", RangeStart: "10.0.0.1", RangeEnd: "10.0.0.10", Gateway: "nope"},
			wantErr: "invalid gateway IP",
		},
		{
			name:    "bad gateway_ip",
			pool:    ExternalPoolConfig{Name: "ok", RangeStart: "10.0.0.1", RangeEnd: "10.0.0.10", Gateway: "10.0.0.254", GatewayIP: "nope"},
			wantErr: "invalid gateway_ip",
		},
		{
			name: "valid",
			pool: ExternalPoolConfig{Name: "ok", RangeStart: "10.0.0.1", RangeEnd: "10.0.0.10", Gateway: "10.0.0.254"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePoolConfig(tt.pool)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExternalIPAM_InitFromConfig(t *testing.T) {
	pool := testPool()
	// Create IPAM twice — second init should be idempotent
	_, _, js := testutil.StartTestJetStream(t)

	// First init
	ipam1, err := NewExternalIPAM(js, []ExternalPoolConfig{pool})
	require.NoError(t, err)

	// Allocate an IP
	ip, _, err := ipam1.AllocateIP("", "", "auto_assign", "", "eni-1", "i-1")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.151", ip)

	// Second init (simulating restart) — should not lose allocation
	ipam2, err := NewExternalIPAM(js, []ExternalPoolConfig{pool})
	require.NoError(t, err)

	record, err := ipam2.GetPoolRecord("wan")
	require.NoError(t, err)
	assert.Contains(t, record.Allocated, "192.168.1.151")
	assert.Contains(t, record.Allocated, "192.168.1.150") // gateway still reserved
}

func TestExternalIPAM_ReleaseNotAllocated(t *testing.T) {
	pool := testPool()
	ipam := setupTestExternalIPAM(t, []ExternalPoolConfig{pool})

	err := ipam.ReleaseIP("wan", "192.168.1.200")
	assert.ErrorContains(t, err, "not allocated")
}

func TestExternalIPAM_NoPoolAvailable(t *testing.T) {
	pool := ExternalPoolConfig{
		Name:       "us-only",
		RangeStart: "10.0.0.1",
		RangeEnd:   "10.0.0.10",
		Gateway:    "10.0.0.254",
		PrefixLen:  24,
		Region:     "us-east-1",
		AZ:         "us-east-1a",
	}
	ipam := setupTestExternalIPAM(t, []ExternalPoolConfig{pool})

	// No pool matches eu-west
	_, _, err := ipam.AllocateIP("eu-west-1", "eu-west-1a", "auto_assign", "", "eni-1", "i-1")
	assert.ErrorContains(t, err, "InsufficientAddressCapacity")
}

func TestExternalIPAM_FindPoolByName_NotFound(t *testing.T) {
	pool := testPool()
	ipam := setupTestExternalIPAM(t, []ExternalPoolConfig{pool})

	// findPoolByName is private, but exercised via AllocateFromPool with unknown pool.
	// The function returns nil when no pool matches, which means static allocation path.
	// We verify by using NewExternalIPAMWithKV to directly check findPoolByName returns nil.
	kv := ipam.kv
	ipam2 := NewExternalIPAMWithKV(kv, []ExternalPoolConfig{pool})
	// AllocateFromPool with a non-existent pool name: findPoolByName returns nil,
	// so pool.IsDHCP() is skipped and static allocation is used.
	// The pool "nonexistent" has no KV record, so getRecord fails.
	_, err := ipam2.AllocateFromPool("nonexistent", "auto_assign", "", "eni-1", "i-1")
	assert.Error(t, err)
}

func TestParseDHCPCDLeasedIP(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name:   "valid lease line",
			output: "br-ext: leased 192.168.1.75 for 1800 seconds",
			want:   "192.168.1.75",
		},
		{
			name:   "lease in middle of multiline output",
			output: "br-ext: soliciting a DHCP lease\nbr-ext: offered 192.168.1.75 from 192.168.1.1\nbr-ext: leased 192.168.1.75 for 1800 seconds\nbr-ext: adding route",
			want:   "192.168.1.75",
		},
		{
			name:   "no lease line",
			output: "br-ext: soliciting a DHCP lease\nbr-ext: timed out",
			want:   "",
		},
		{
			name:   "empty output",
			output: "",
			want:   "",
		},
		{
			name:   "malformed line - no space after IP",
			output: ": leased 192.168.1.75",
			want:   "",
		},
		{
			name:   "empty before colon",
			output: ": leased 192.168.1.75 for 600 seconds",
			want:   "",
		},
		{
			name:   "lease keyword but no IP after",
			output: "br-ext: leased ",
			want:   "",
		},
		{
			name:   "different bridge name",
			output: "br-wan: leased 10.0.0.50 for 3600 seconds",
			want:   "10.0.0.50",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDHCPCDLeasedIP(tt.output)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExternalPoolConfig_IsDHCP(t *testing.T) {
	p := ExternalPoolConfig{Source: "dhcp"}
	assert.True(t, p.IsDHCP())

	p2 := ExternalPoolConfig{Source: "static"}
	assert.False(t, p2.IsDHCP())

	p3 := ExternalPoolConfig{}
	assert.False(t, p3.IsDHCP())
}

func TestValidatePoolConfig_DHCPPool(t *testing.T) {
	// DHCP pools don't need range_start/range_end
	pool := ExternalPoolConfig{
		Name:    "dhcp-pool",
		Source:  "dhcp",
		Gateway: "192.168.1.1",
	}
	err := ValidatePoolConfig(pool)
	assert.NoError(t, err)
}
