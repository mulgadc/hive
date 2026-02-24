package vpcd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mulgadc/hive/hive/services/vpcd/nbdb"
	"github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/model"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
)

// transactOps executes a set of OVSDB operations as a single transaction,
// checking both the RPC error and individual operation results.
func (c *LiveOVNClient) transactOps(ctx context.Context, ops []ovsdb.Operation) error {
	results, err := c.client.Transact(ctx, ops...)
	if err != nil {
		return err
	}
	_, err = ovsdb.CheckOperationResults(results, ops)
	if err != nil {
		// Log detailed per-operation errors for debugging
		for i, r := range results {
			if r.Error != "" {
				opTable := ""
				if i < len(ops) {
					opTable = fmt.Sprintf("%s on %s", ops[i].Op, ops[i].Table)
				}
				slog.Error("OVSDB operation failed", "index", i, "op", opTable, "error", r.Error, "details", r.Details)
			}
		}
	}
	return err
}

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
	GetLogicalRouterPort(ctx context.Context, name string) (*nbdb.LogicalRouterPort, error)

	// DHCP Options
	CreateDHCPOptions(ctx context.Context, opts *nbdb.DHCPOptions) (string, error)
	DeleteDHCPOptions(ctx context.Context, uuid string) error
	FindDHCPOptionsByCIDR(ctx context.Context, cidr string) (*nbdb.DHCPOptions, error)
	FindDHCPOptionsByExternalID(ctx context.Context, key, value string) (*nbdb.DHCPOptions, error)
	ListDHCPOptions(ctx context.Context) ([]nbdb.DHCPOptions, error)

	// NAT rules
	AddNAT(ctx context.Context, routerName string, nat *nbdb.NAT) error
	DeleteNAT(ctx context.Context, routerName string, natType, logicalIP string) error

	// Static routes
	AddStaticRoute(ctx context.Context, routerName string, route *nbdb.LogicalRouterStaticRoute) error
	DeleteStaticRoute(ctx context.Context, routerName string, ipPrefix string) error
}

// namedUUID generates a valid OVSDB named-uuid from a prefix and name.
// OVSDB named-uuids must match [_a-zA-Z][_a-zA-Z0-9]* — hyphens and dots
// are replaced with underscores.
func namedUUID(prefix, name string) string {
	s := prefix + name
	result := make([]byte, len(s))
	for i := range s {
		if s[i] == '-' || s[i] == '.' || s[i] == '/' {
			result[i] = '_'
		} else {
			result[i] = s[i]
		}
	}
	return string(result)
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
	err = c.transactOps(ctx, ops)
	if err != nil {
		return fmt.Errorf("create logical switch transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) DeleteLogicalSwitch(ctx context.Context, name string) error {
	ls, err := c.GetLogicalSwitch(ctx, name)
	if err != nil {
		return fmt.Errorf("delete logical switch lookup: %w", err)
	}
	ops, err := c.client.Where(ls).Delete()
	if err != nil {
		return fmt.Errorf("delete logical switch ops: %w", err)
	}
	err = c.transactOps(ctx, ops)
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
	// Set a named UUID so the port can be referenced in the same transaction
	if lsp.UUID == "" {
		lsp.UUID = namedUUID("lsp_", lsp.Name)
	}

	// Create the port
	createOps, err := c.client.Create(lsp)
	if err != nil {
		return fmt.Errorf("create logical switch port ops: %w", err)
	}

	// Look up the switch to get its UUID for the Where clause
	ls, err := c.GetLogicalSwitch(ctx, switchName)
	if err != nil {
		return fmt.Errorf("get logical switch for port add: %w", err)
	}

	// Add the port to the switch's ports set (uses named UUID from Create)
	mutateOps, err := c.client.Where(ls).Mutate(ls, model.Mutation{
		Field:   &ls.Ports,
		Mutator: "insert",
		Value:   []string{lsp.UUID},
	})
	if err != nil {
		return fmt.Errorf("mutate logical switch ports ops: %w", err)
	}

	ops := append(createOps, mutateOps...)
	err = c.transactOps(ctx, ops)
	if err != nil {
		return fmt.Errorf("create logical switch port transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) DeleteLogicalSwitchPort(ctx context.Context, switchName string, portName string) error {
	// Look up the port to get its UUID
	lsp, err := c.GetLogicalSwitchPort(ctx, portName)
	if err != nil {
		return fmt.Errorf("get logical switch port for delete: %w", err)
	}

	// Look up the switch to get its UUID
	ls, err := c.GetLogicalSwitch(ctx, switchName)
	if err != nil {
		return fmt.Errorf("get logical switch for port delete: %w", err)
	}

	// Remove the port from the switch's ports set
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
	err = c.transactOps(ctx, ops)
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
	// Ensure we have the UUID for the Where clause
	if lsp.UUID == "" {
		existing, err := c.GetLogicalSwitchPort(ctx, lsp.Name)
		if err != nil {
			return fmt.Errorf("get logical switch port for update: %w", err)
		}
		lsp.UUID = existing.UUID
	}
	ops, err := c.client.Where(lsp).Update(lsp)
	if err != nil {
		return fmt.Errorf("update logical switch port ops: %w", err)
	}
	err = c.transactOps(ctx, ops)
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
	err = c.transactOps(ctx, ops)
	if err != nil {
		return fmt.Errorf("create logical router transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) DeleteLogicalRouter(ctx context.Context, name string) error {
	lr, err := c.GetLogicalRouter(ctx, name)
	if err != nil {
		return fmt.Errorf("delete logical router lookup: %w", err)
	}
	ops, err := c.client.Where(lr).Delete()
	if err != nil {
		return fmt.Errorf("delete logical router ops: %w", err)
	}
	err = c.transactOps(ctx, ops)
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
	// Set a named UUID so the port can be referenced in the same transaction
	if lrp.UUID == "" {
		lrp.UUID = namedUUID("lrp_", lrp.Name)
	}

	createOps, err := c.client.Create(lrp)
	if err != nil {
		return fmt.Errorf("create logical router port ops: %w", err)
	}

	// Look up the router to get its UUID for the Where clause
	lr, err := c.GetLogicalRouter(ctx, routerName)
	if err != nil {
		return fmt.Errorf("get logical router for port add: %w", err)
	}

	mutateOps, err := c.client.Where(lr).Mutate(lr, model.Mutation{
		Field:   &lr.Ports,
		Mutator: "insert",
		Value:   []string{lrp.UUID},
	})
	if err != nil {
		return fmt.Errorf("mutate logical router ports ops: %w", err)
	}

	ops := append(createOps, mutateOps...)
	err = c.transactOps(ctx, ops)
	if err != nil {
		return fmt.Errorf("create logical router port transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) DeleteLogicalRouterPort(ctx context.Context, routerName string, portName string) error {
	// Look up the port to get its UUID
	lrp, err := c.GetLogicalRouterPort(ctx, portName)
	if err != nil {
		return fmt.Errorf("get logical router port for delete: %w", err)
	}

	// Look up the router to get its UUID
	lr, err := c.GetLogicalRouter(ctx, routerName)
	if err != nil {
		return fmt.Errorf("get logical router for port delete: %w", err)
	}

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
	err = c.transactOps(ctx, ops)
	if err != nil {
		return fmt.Errorf("delete logical router port transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) GetLogicalRouterPort(ctx context.Context, name string) (*nbdb.LogicalRouterPort, error) {
	var ports []nbdb.LogicalRouterPort
	err := c.client.WhereCache(func(lrp *nbdb.LogicalRouterPort) bool {
		return lrp.Name == name
	}).List(ctx, &ports)
	if err != nil {
		return nil, fmt.Errorf("get logical router port: %w", err)
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("logical router port %q not found", name)
	}
	return &ports[0], nil
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
	if _, err := ovsdb.CheckOperationResults(results, ops); err != nil {
		return "", fmt.Errorf("create DHCP options check: %w", err)
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
	err = c.transactOps(ctx, ops)
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

func (c *LiveOVNClient) AddNAT(ctx context.Context, routerName string, nat *nbdb.NAT) error {
	// Set a named UUID so the NAT can be referenced in the same transaction
	if nat.UUID == "" {
		nat.UUID = namedUUID("nat_", nat.Type+"_"+nat.LogicalIP)
	}

	createOps, err := c.client.Create(nat)
	if err != nil {
		return fmt.Errorf("create NAT ops: %w", err)
	}

	lr, err := c.GetLogicalRouter(ctx, routerName)
	if err != nil {
		return fmt.Errorf("get logical router for NAT add: %w", err)
	}

	mutateOps, err := c.client.Where(lr).Mutate(lr, model.Mutation{
		Field:   &lr.NAT,
		Mutator: "insert",
		Value:   []string{nat.UUID},
	})
	if err != nil {
		return fmt.Errorf("mutate router NAT ops: %w", err)
	}

	ops := append(createOps, mutateOps...)
	err = c.transactOps(ctx, ops)
	if err != nil {
		return fmt.Errorf("add NAT transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) DeleteNAT(ctx context.Context, routerName string, natType, logicalIP string) error {
	// Find the NAT entry by type and logical IP
	var nats []nbdb.NAT
	err := c.client.WhereCache(func(n *nbdb.NAT) bool {
		return n.Type == natType && n.LogicalIP == logicalIP
	}).List(ctx, &nats)
	if err != nil {
		return fmt.Errorf("find NAT: %w", err)
	}
	if len(nats) == 0 {
		return fmt.Errorf("NAT %s %s not found", natType, logicalIP)
	}

	nat := &nats[0]
	lr, err := c.GetLogicalRouter(ctx, routerName)
	if err != nil {
		return fmt.Errorf("get logical router for NAT delete: %w", err)
	}

	mutateOps, err := c.client.Where(lr).Mutate(lr, model.Mutation{
		Field:   &lr.NAT,
		Mutator: "delete",
		Value:   []string{nat.UUID},
	})
	if err != nil {
		return fmt.Errorf("mutate router NAT ops: %w", err)
	}

	deleteOps, err := c.client.Where(nat).Delete()
	if err != nil {
		return fmt.Errorf("delete NAT ops: %w", err)
	}

	ops := append(mutateOps, deleteOps...)
	err = c.transactOps(ctx, ops)
	if err != nil {
		return fmt.Errorf("delete NAT transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) AddStaticRoute(ctx context.Context, routerName string, route *nbdb.LogicalRouterStaticRoute) error {
	// Set a named UUID so the route can be referenced in the same transaction
	if route.UUID == "" {
		route.UUID = namedUUID("route_", route.IPPrefix)
	}

	createOps, err := c.client.Create(route)
	if err != nil {
		return fmt.Errorf("create static route ops: %w", err)
	}

	lr, err := c.GetLogicalRouter(ctx, routerName)
	if err != nil {
		return fmt.Errorf("get logical router for route add: %w", err)
	}

	mutateOps, err := c.client.Where(lr).Mutate(lr, model.Mutation{
		Field:   &lr.StaticRoutes,
		Mutator: "insert",
		Value:   []string{route.UUID},
	})
	if err != nil {
		return fmt.Errorf("mutate router static routes ops: %w", err)
	}

	ops := append(createOps, mutateOps...)
	err = c.transactOps(ctx, ops)
	if err != nil {
		return fmt.Errorf("add static route transact: %w", err)
	}
	return nil
}

func (c *LiveOVNClient) DeleteStaticRoute(ctx context.Context, routerName string, ipPrefix string) error {
	// Find the route by IP prefix
	var routes []nbdb.LogicalRouterStaticRoute
	err := c.client.WhereCache(func(r *nbdb.LogicalRouterStaticRoute) bool {
		return r.IPPrefix == ipPrefix
	}).List(ctx, &routes)
	if err != nil {
		return fmt.Errorf("find static route: %w", err)
	}
	if len(routes) == 0 {
		return fmt.Errorf("static route %s not found", ipPrefix)
	}

	route := &routes[0]
	lr, err := c.GetLogicalRouter(ctx, routerName)
	if err != nil {
		return fmt.Errorf("get logical router for route delete: %w", err)
	}

	mutateOps, err := c.client.Where(lr).Mutate(lr, model.Mutation{
		Field:   &lr.StaticRoutes,
		Mutator: "delete",
		Value:   []string{route.UUID},
	})
	if err != nil {
		return fmt.Errorf("mutate router static routes ops: %w", err)
	}

	deleteOps, err := c.client.Where(route).Delete()
	if err != nil {
		return fmt.Errorf("delete static route ops: %w", err)
	}

	ops := append(mutateOps, deleteOps...)
	err = c.transactOps(ctx, ops)
	if err != nil {
		return fmt.Errorf("delete static route transact: %w", err)
	}
	return nil
}
