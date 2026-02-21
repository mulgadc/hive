package vpcd

import (
	"context"
	"fmt"
	"sync"

	"github.com/mulgadc/hive/hive/services/vpcd/nbdb"
	"github.com/mulgadc/hive/hive/utils"
)

// MockOVNClient implements OVNClient with in-memory storage for testing.
type MockOVNClient struct {
	mu        sync.Mutex
	connected bool

	switches    map[string]*nbdb.LogicalSwitch
	ports       map[string]*nbdb.LogicalSwitchPort
	routers     map[string]*nbdb.LogicalRouter
	routerPorts map[string]*nbdb.LogicalRouterPort
	dhcpOpts    map[string]*nbdb.DHCPOptions
}

// NewMockOVNClient creates a new MockOVNClient for testing.
func NewMockOVNClient() *MockOVNClient {
	return &MockOVNClient{
		switches:    make(map[string]*nbdb.LogicalSwitch),
		ports:       make(map[string]*nbdb.LogicalSwitchPort),
		routers:     make(map[string]*nbdb.LogicalRouter),
		routerPorts: make(map[string]*nbdb.LogicalRouterPort),
		dhcpOpts:    make(map[string]*nbdb.DHCPOptions),
	}
}

func (m *MockOVNClient) Connect(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = true
	return nil
}

func (m *MockOVNClient) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = false
}

func (m *MockOVNClient) Connected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

// Logical Switch

func (m *MockOVNClient) CreateLogicalSwitch(_ context.Context, ls *nbdb.LogicalSwitch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.switches[ls.Name]; exists {
		return fmt.Errorf("logical switch %q already exists", ls.Name)
	}
	if ls.UUID == "" {
		ls.UUID = utils.GenerateResourceID("ovn")
	}
	stored := *ls
	m.switches[ls.Name] = &stored
	return nil
}

func (m *MockOVNClient) DeleteLogicalSwitch(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.switches[name]; !exists {
		return fmt.Errorf("logical switch %q not found", name)
	}
	delete(m.switches, name)
	return nil
}

func (m *MockOVNClient) GetLogicalSwitch(_ context.Context, name string) (*nbdb.LogicalSwitch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, exists := m.switches[name]
	if !exists {
		return nil, fmt.Errorf("logical switch %q not found", name)
	}
	result := *ls
	return &result, nil
}

func (m *MockOVNClient) ListLogicalSwitches(_ context.Context) ([]nbdb.LogicalSwitch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]nbdb.LogicalSwitch, 0, len(m.switches))
	for _, ls := range m.switches {
		result = append(result, *ls)
	}
	return result, nil
}

// Logical Switch Port

func (m *MockOVNClient) CreateLogicalSwitchPort(_ context.Context, switchName string, lsp *nbdb.LogicalSwitchPort) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, exists := m.switches[switchName]
	if !exists {
		return fmt.Errorf("logical switch %q not found", switchName)
	}
	if _, exists := m.ports[lsp.Name]; exists {
		return fmt.Errorf("logical switch port %q already exists", lsp.Name)
	}
	if lsp.UUID == "" {
		lsp.UUID = utils.GenerateResourceID("ovn")
	}
	stored := *lsp
	m.ports[lsp.Name] = &stored
	ls.Ports = append(ls.Ports, lsp.UUID)
	return nil
}

func (m *MockOVNClient) DeleteLogicalSwitchPort(_ context.Context, switchName string, portName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	port, exists := m.ports[portName]
	if !exists {
		return fmt.Errorf("logical switch port %q not found", portName)
	}
	ls, exists := m.switches[switchName]
	if !exists {
		return fmt.Errorf("logical switch %q not found", switchName)
	}
	// Remove port UUID from switch's ports list
	for i, uuid := range ls.Ports {
		if uuid == port.UUID {
			ls.Ports = append(ls.Ports[:i], ls.Ports[i+1:]...)
			break
		}
	}
	delete(m.ports, portName)
	return nil
}

func (m *MockOVNClient) GetLogicalSwitchPort(_ context.Context, name string) (*nbdb.LogicalSwitchPort, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lsp, exists := m.ports[name]
	if !exists {
		return nil, fmt.Errorf("logical switch port %q not found", name)
	}
	result := *lsp
	return &result, nil
}

func (m *MockOVNClient) UpdateLogicalSwitchPort(_ context.Context, lsp *nbdb.LogicalSwitchPort) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.ports[lsp.Name]; !exists {
		return fmt.Errorf("logical switch port %q not found", lsp.Name)
	}
	stored := *lsp
	m.ports[lsp.Name] = &stored
	return nil
}

// Logical Router

func (m *MockOVNClient) CreateLogicalRouter(_ context.Context, lr *nbdb.LogicalRouter) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.routers[lr.Name]; exists {
		return fmt.Errorf("logical router %q already exists", lr.Name)
	}
	if lr.UUID == "" {
		lr.UUID = utils.GenerateResourceID("ovn")
	}
	stored := *lr
	m.routers[lr.Name] = &stored
	return nil
}

func (m *MockOVNClient) DeleteLogicalRouter(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.routers[name]; !exists {
		return fmt.Errorf("logical router %q not found", name)
	}
	delete(m.routers, name)
	return nil
}

func (m *MockOVNClient) GetLogicalRouter(_ context.Context, name string) (*nbdb.LogicalRouter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lr, exists := m.routers[name]
	if !exists {
		return nil, fmt.Errorf("logical router %q not found", name)
	}
	result := *lr
	return &result, nil
}

func (m *MockOVNClient) ListLogicalRouters(_ context.Context) ([]nbdb.LogicalRouter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]nbdb.LogicalRouter, 0, len(m.routers))
	for _, lr := range m.routers {
		result = append(result, *lr)
	}
	return result, nil
}

// Logical Router Port

func (m *MockOVNClient) CreateLogicalRouterPort(_ context.Context, routerName string, lrp *nbdb.LogicalRouterPort) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lr, exists := m.routers[routerName]
	if !exists {
		return fmt.Errorf("logical router %q not found", routerName)
	}
	if _, exists := m.routerPorts[lrp.Name]; exists {
		return fmt.Errorf("logical router port %q already exists", lrp.Name)
	}
	if lrp.UUID == "" {
		lrp.UUID = utils.GenerateResourceID("ovn")
	}
	stored := *lrp
	m.routerPorts[lrp.Name] = &stored
	lr.Ports = append(lr.Ports, lrp.UUID)
	return nil
}

func (m *MockOVNClient) DeleteLogicalRouterPort(_ context.Context, routerName string, portName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	port, exists := m.routerPorts[portName]
	if !exists {
		return fmt.Errorf("logical router port %q not found", portName)
	}
	lr, exists := m.routers[routerName]
	if !exists {
		return fmt.Errorf("logical router %q not found", routerName)
	}
	for i, uuid := range lr.Ports {
		if uuid == port.UUID {
			lr.Ports = append(lr.Ports[:i], lr.Ports[i+1:]...)
			break
		}
	}
	delete(m.routerPorts, portName)
	return nil
}

// DHCP Options

func (m *MockOVNClient) CreateDHCPOptions(_ context.Context, opts *nbdb.DHCPOptions) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if opts.UUID == "" {
		opts.UUID = utils.GenerateResourceID("dhcp")
	}
	stored := *opts
	m.dhcpOpts[opts.UUID] = &stored
	return opts.UUID, nil
}

func (m *MockOVNClient) DeleteDHCPOptions(_ context.Context, uuid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.dhcpOpts[uuid]; !exists {
		return fmt.Errorf("DHCP options %q not found", uuid)
	}
	delete(m.dhcpOpts, uuid)
	return nil
}

func (m *MockOVNClient) FindDHCPOptionsByCIDR(_ context.Context, cidr string) (*nbdb.DHCPOptions, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, opts := range m.dhcpOpts {
		if opts.CIDR == cidr {
			result := *opts
			return &result, nil
		}
	}
	return nil, fmt.Errorf("DHCP options for CIDR %q not found", cidr)
}

func (m *MockOVNClient) FindDHCPOptionsByExternalID(_ context.Context, key, value string) (*nbdb.DHCPOptions, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, opts := range m.dhcpOpts {
		if opts.ExternalIDs[key] == value {
			result := *opts
			return &result, nil
		}
	}
	return nil, fmt.Errorf("DHCP options with external_id %s=%s not found", key, value)
}

func (m *MockOVNClient) ListDHCPOptions(_ context.Context) ([]nbdb.DHCPOptions, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]nbdb.DHCPOptions, 0, len(m.dhcpOpts))
	for _, opts := range m.dhcpOpts {
		result = append(result, *opts)
	}
	return result, nil
}
