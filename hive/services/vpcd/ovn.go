package vpcd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mulgadc/hive/hive/services/vpcd/nbdb"
	"github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/model"
)

// OVNClient defines the interface for interacting with the OVN Northbound Database.
// This abstraction allows for mock implementations in tests.
type OVNClient interface {
	// Connection lifecycle
	Connect(ctx context.Context) error
	Close()
	Connected() bool

	// Logical Switch (subnet)
	CreateLogicalSwitch(ctx context.Context, ls *nbdb.LogicalSwitch) error
	DeleteLogicalSwitch(ctx context.Context, name string) error
	GetLogicalSwitch(ctx context.Context, name string) (*nbdb.LogicalSwitch, error)
	ListLogicalSwitches(ctx context.Context) ([]nbdb.LogicalSwitch, error)

	// Logical Switch Port (VM/ENI)
	CreateLogicalSwitchPort(ctx context.Context, switchName string, lsp *nbdb.LogicalSwitchPort) error
	DeleteLogicalSwitchPort(ctx context.Context, switchName string, portName string) error
	GetLogicalSwitchPort(ctx context.Context, name string) (*nbdb.LogicalSwitchPort, error)
	UpdateLogicalSwitchPort(ctx context.Context, lsp *nbdb.LogicalSwitchPort) error

	// Logical Router (VPC router)
	CreateLogicalRouter(ctx context.Context, lr *nbdb.LogicalRouter) error
	DeleteLogicalRouter(ctx context.Context, name string) error
	GetLogicalRouter(ctx context.Context, name string) (*nbdb.LogicalRouter, error)
	ListLogicalRouters(ctx context.Context) ([]nbdb.LogicalRouter, error)

	// Logical Router Port
	CreateLogicalRouterPort(ctx context.Context, routerName string, lrp *nbdb.LogicalRouterPort) error
	DeleteLogicalRouterPort(ctx context.Context, routerName string, portName string) error

	// DHCP Options
	CreateDHCPOptions(ctx context.Context, opts *nbdb.DHCPOptions) (string, error)
	DeleteDHCPOptions(ctx context.Context, uuid string) error
	FindDHCPOptionsByCIDR(ctx context.Context, cidr string) (*nbdb.DHCPOptions, error)
	FindDHCPOptionsByExternalID(ctx context.Context, key, value string) (*nbdb.DHCPOptions, error)
	ListDHCPOptions(ctx context.Context) ([]nbdb.DHCPOptions, error)
}

// LiveOVNClient implements OVNClient using libovsdb against a real OVN NB DB.
type LiveOVNClient struct {
	endpoint string
	client   client.Client
}

// NewLiveOVNClient creates a new LiveOVNClient targeting the given OVN NB DB endpoint.
// The endpoint should be in the format "tcp:host:port" or "unix:/path/to/socket".
func NewLiveOVNClient(endpoint string) *LiveOVNClient {
	return &LiveOVNClient{endpoint: endpoint}
}

func (c *LiveOVNClient) Connect(ctx context.Context) error {
	dbModel, err := nbdb.FullDatabaseModel()
	if err != nil {
		return fmt.Errorf("failed to create database model: %w", err)
	}

	ovn, err := client.NewOVSDBClient(dbModel, client.WithEndpoint(c.endpoint))
	if err != nil {
		return fmt.Errorf("failed to create OVSDB client: %w", err)
	}

	if err := ovn.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to OVN NB DB at %s: %w", c.endpoint, err)
	}

	_, err = ovn.MonitorAll(ctx)
	if err != nil {
		ovn.Close()
		return fmt.Errorf("failed to monitor OVN NB DB: %w", err)
	}

	c.client = ovn
	slog.Info("Connected to OVN NB DB", "endpoint", c.endpoint)
	return nil
}

func (c *LiveOVNClient) Close() {
	if c.client != nil {
		c.client.Close()
		slog.Info("Disconnected from OVN NB DB")
	}
}

func (c *LiveOVNClient) Connected() bool {
	return c.client != nil
}

func (c *LiveOVNClient) CreateLogicalSwitch(ctx context.Context, ls *nbdb.LogicalSwitch) error {
	ops, err := c.client.Create(ls)
	if err != nil {
		return fmt.Errorf("create logical switch ops: %w", err)
	}
	_, err = c.client.Transact(ctx, ops...)
	if err != nil {
		return fmt.Errorf("create logical switch transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) DeleteLogicalSwitch(ctx context.Context, name string) error {
	ls := &nbdb.LogicalSwitch{Name: name}
	ops, err := c.client.Where(ls).Delete()
	if err != nil {
		return fmt.Errorf("delete logical switch ops: %w", err)
	}
	_, err = c.client.Transact(ctx, ops...)
	if err != nil {
		return fmt.Errorf("delete logical switch transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) GetLogicalSwitch(ctx context.Context, name string) (*nbdb.LogicalSwitch, error) {
	var switches []nbdb.LogicalSwitch
	err := c.client.WhereCache(func(ls *nbdb.LogicalSwitch) bool {
		return ls.Name == name
	}).List(ctx, &switches)
	if err != nil {
		return nil, fmt.Errorf("get logical switch: %w", err)
	}
	if len(switches) == 0 {
		return nil, fmt.Errorf("logical switch %q not found", name)
	}
	return &switches[0], nil
}

func (c *LiveOVNClient) ListLogicalSwitches(ctx context.Context) ([]nbdb.LogicalSwitch, error) {
	var switches []nbdb.LogicalSwitch
	err := c.client.List(ctx, &switches)
	if err != nil {
		return nil, fmt.Errorf("list logical switches: %w", err)
	}
	return switches, nil
}

func (c *LiveOVNClient) CreateLogicalSwitchPort(ctx context.Context, switchName string, lsp *nbdb.LogicalSwitchPort) error {
	// Create the port
	createOps, err := c.client.Create(lsp)
	if err != nil {
		return fmt.Errorf("create logical switch port ops: %w", err)
	}

	// Add the port to the switch's ports set
	ls := &nbdb.LogicalSwitch{Name: switchName}
	mutateOps, err := c.client.Where(ls).Mutate(ls, model.Mutation{
		Field:   &ls.Ports,
		Mutator: "insert",
		Value:   []string{lsp.UUID},
	})
	if err != nil {
		return fmt.Errorf("mutate logical switch ports ops: %w", err)
	}

	ops := append(createOps, mutateOps...)
	_, err = c.client.Transact(ctx, ops...)
	if err != nil {
		return fmt.Errorf("create logical switch port transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) DeleteLogicalSwitchPort(ctx context.Context, switchName string, portName string) error {
	lsp := &nbdb.LogicalSwitchPort{Name: portName}

	// Remove the port from the switch's ports set
	ls := &nbdb.LogicalSwitch{Name: switchName}
	mutateOps, err := c.client.Where(ls).Mutate(ls, model.Mutation{
		Field:   &ls.Ports,
		Mutator: "delete",
		Value:   []string{lsp.UUID},
	})
	if err != nil {
		return fmt.Errorf("mutate logical switch ports ops: %w", err)
	}

	// Delete the port
	deleteOps, err := c.client.Where(lsp).Delete()
	if err != nil {
		return fmt.Errorf("delete logical switch port ops: %w", err)
	}

	ops := append(mutateOps, deleteOps...)
	_, err = c.client.Transact(ctx, ops...)
	if err != nil {
		return fmt.Errorf("delete logical switch port transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) GetLogicalSwitchPort(ctx context.Context, name string) (*nbdb.LogicalSwitchPort, error) {
	var ports []nbdb.LogicalSwitchPort
	err := c.client.WhereCache(func(lsp *nbdb.LogicalSwitchPort) bool {
		return lsp.Name == name
	}).List(ctx, &ports)
	if err != nil {
		return nil, fmt.Errorf("get logical switch port: %w", err)
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("logical switch port %q not found", name)
	}
	return &ports[0], nil
}

func (c *LiveOVNClient) UpdateLogicalSwitchPort(ctx context.Context, lsp *nbdb.LogicalSwitchPort) error {
	ops, err := c.client.Where(lsp).Update(lsp)
	if err != nil {
		return fmt.Errorf("update logical switch port ops: %w", err)
	}
	_, err = c.client.Transact(ctx, ops...)
	if err != nil {
		return fmt.Errorf("update logical switch port transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) CreateLogicalRouter(ctx context.Context, lr *nbdb.LogicalRouter) error {
	ops, err := c.client.Create(lr)
	if err != nil {
		return fmt.Errorf("create logical router ops: %w", err)
	}
	_, err = c.client.Transact(ctx, ops...)
	if err != nil {
		return fmt.Errorf("create logical router transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) DeleteLogicalRouter(ctx context.Context, name string) error {
	lr := &nbdb.LogicalRouter{Name: name}
	ops, err := c.client.Where(lr).Delete()
	if err != nil {
		return fmt.Errorf("delete logical router ops: %w", err)
	}
	_, err = c.client.Transact(ctx, ops...)
	if err != nil {
		return fmt.Errorf("delete logical router transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) GetLogicalRouter(ctx context.Context, name string) (*nbdb.LogicalRouter, error) {
	var routers []nbdb.LogicalRouter
	err := c.client.WhereCache(func(lr *nbdb.LogicalRouter) bool {
		return lr.Name == name
	}).List(ctx, &routers)
	if err != nil {
		return nil, fmt.Errorf("get logical router: %w", err)
	}
	if len(routers) == 0 {
		return nil, fmt.Errorf("logical router %q not found", name)
	}
	return &routers[0], nil
}

func (c *LiveOVNClient) ListLogicalRouters(ctx context.Context) ([]nbdb.LogicalRouter, error) {
	var routers []nbdb.LogicalRouter
	err := c.client.List(ctx, &routers)
	if err != nil {
		return nil, fmt.Errorf("list logical routers: %w", err)
	}
	return routers, nil
}

func (c *LiveOVNClient) CreateLogicalRouterPort(ctx context.Context, routerName string, lrp *nbdb.LogicalRouterPort) error {
	createOps, err := c.client.Create(lrp)
	if err != nil {
		return fmt.Errorf("create logical router port ops: %w", err)
	}

	lr := &nbdb.LogicalRouter{Name: routerName}
	mutateOps, err := c.client.Where(lr).Mutate(lr, model.Mutation{
		Field:   &lr.Ports,
		Mutator: "insert",
		Value:   []string{lrp.UUID},
	})
	if err != nil {
		return fmt.Errorf("mutate logical router ports ops: %w", err)
	}

	ops := append(createOps, mutateOps...)
	_, err = c.client.Transact(ctx, ops...)
	if err != nil {
		return fmt.Errorf("create logical router port transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) DeleteLogicalRouterPort(ctx context.Context, routerName string, portName string) error {
	lrp := &nbdb.LogicalRouterPort{Name: portName}

	lr := &nbdb.LogicalRouter{Name: routerName}
	mutateOps, err := c.client.Where(lr).Mutate(lr, model.Mutation{
		Field:   &lr.Ports,
		Mutator: "delete",
		Value:   []string{lrp.UUID},
	})
	if err != nil {
		return fmt.Errorf("mutate logical router ports ops: %w", err)
	}

	deleteOps, err := c.client.Where(lrp).Delete()
	if err != nil {
		return fmt.Errorf("delete logical router port ops: %w", err)
	}

	ops := append(mutateOps, deleteOps...)
	_, err = c.client.Transact(ctx, ops...)
	if err != nil {
		return fmt.Errorf("delete logical router port transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) CreateDHCPOptions(ctx context.Context, opts *nbdb.DHCPOptions) (string, error) {
	ops, err := c.client.Create(opts)
	if err != nil {
		return "", fmt.Errorf("create DHCP options ops: %w", err)
	}
	results, err := c.client.Transact(ctx, ops...)
	if err != nil {
		return "", fmt.Errorf("create DHCP options transact: %w", err)
	}
	if len(results) > 0 {
		return results[0].UUID.GoUUID, nil
	}
	return "", nil
}

func (c *LiveOVNClient) DeleteDHCPOptions(ctx context.Context, uuid string) error {
	opts := &nbdb.DHCPOptions{UUID: uuid}
	ops, err := c.client.Where(opts).Delete()
	if err != nil {
		return fmt.Errorf("delete DHCP options ops: %w", err)
	}
	_, err = c.client.Transact(ctx, ops...)
	if err != nil {
		return fmt.Errorf("delete DHCP options transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) FindDHCPOptionsByCIDR(ctx context.Context, cidr string) (*nbdb.DHCPOptions, error) {
	var options []nbdb.DHCPOptions
	err := c.client.WhereCache(func(o *nbdb.DHCPOptions) bool {
		return o.CIDR == cidr
	}).List(ctx, &options)
	if err != nil {
		return nil, fmt.Errorf("find DHCP options by CIDR: %w", err)
	}
	if len(options) == 0 {
		return nil, fmt.Errorf("DHCP options for CIDR %q not found", cidr)
	}
	return &options[0], nil
}

func (c *LiveOVNClient) FindDHCPOptionsByExternalID(ctx context.Context, key, value string) (*nbdb.DHCPOptions, error) {
	var options []nbdb.DHCPOptions
	err := c.client.WhereCache(func(o *nbdb.DHCPOptions) bool {
		return o.ExternalIDs[key] == value
	}).List(ctx, &options)
	if err != nil {
		return nil, fmt.Errorf("find DHCP options by external_id %s=%s: %w", key, value, err)
	}
	if len(options) == 0 {
		return nil, fmt.Errorf("DHCP options with external_id %s=%s not found", key, value)
	}
	return &options[0], nil
}

func (c *LiveOVNClient) ListDHCPOptions(ctx context.Context) ([]nbdb.DHCPOptions, error) {
	var options []nbdb.DHCPOptions
	err := c.client.List(ctx, &options)
	if err != nil {
		return nil, fmt.Errorf("list DHCP options: %w", err)
	}
	return options, nil
}
