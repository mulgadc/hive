package dhcp

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/mdlayher/packet"
	"golang.org/x/sys/unix"
)

// dhcpClientPort is the IANA-assigned UDP port for DHCP clients.
const dhcpClientPort = 68

// openPromiscBridgeConn opens an AF_PACKET DGRAM socket on iface filtered
// to IPv4 and enables PACKET_MR_PROMISC on it. Equivalent to nclient4's
// own NewRawUDPConn but with promisc on.
//
// Why promisc: vpcd uses a synthetic per-tenant chaddr (e.g. 02:f3:...)
// that never matches the bridge's MAC. Many real-world DHCP servers
// ignore the BOOTP BROADCAST flag and reply with a unicast OFFER addressed
// to that chaddr. A Linux bridge floods unknown unicast to slave ports
// but does NOT pass it up to the bridge netdev unless IFF_PROMISC is set,
// so the AF_PACKET socket misses the OFFER and DORA times out. Promisc
// flips IFF_PROMISC for the lifetime of the socket (per-DORA), and the
// kernel refcounts it so it auto-clears on Close.
func openPromiscBridgeConn(iface string) (net.PacketConn, net.HardwareAddr, error) {
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, nil, fmt.Errorf("lookup iface %s: %w", iface, err)
	}
	raw, err := packet.Listen(ifc, packet.Datagram, unix.ETH_P_IP, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("packet.Listen on %s: %w", iface, err)
	}
	if err := raw.SetPromiscuous(true); err != nil {
		_ = raw.Close()
		return nil, nil, fmt.Errorf("set promisc on %s: %w", iface, err)
	}
	return nclient4.NewBroadcastUDPConn(raw, &net.UDPAddr{Port: dhcpClientPort}), ifc.HardwareAddr, nil
}

// NClient4Client is the production DHCP client backed by
// github.com/insomniacslk/dhcp/dhcpv4/nclient4. Each Acquire/Renew/Release
// opens an AF_PACKET socket on the target bridge for the duration of the
// handshake and closes it when done — no long-lived per-lease process.
type NClient4Client struct {
	timeout time.Duration
	retry   int
}

var _ Client = (*NClient4Client)(nil)

// NewNClient4 creates an NClient4Client with sensible DORA defaults.
func NewNClient4(timeout time.Duration, retry int) *NClient4Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if retry <= 0 {
		retry = 3
	}
	return &NClient4Client{timeout: timeout, retry: retry}
}

func (c *NClient4Client) Acquire(ctx context.Context, req AcquireRequest) (*Lease, error) {
	if req.Bridge == "" {
		return nil, fmt.Errorf("dhcp acquire: bridge is required")
	}
	if len(req.HWAddr) == 0 {
		return nil, fmt.Errorf("dhcp acquire: hw_addr is required")
	}

	conn, ifaceMAC, err := openPromiscBridgeConn(req.Bridge)
	if err != nil {
		return nil, fmt.Errorf("open promisc conn on %s: %w", req.Bridge, err)
	}
	client, err := nclient4.NewWithConn(conn, ifaceMAC,
		nclient4.WithHWAddr(req.HWAddr),
		nclient4.WithTimeout(c.timeout),
		nclient4.WithRetry(c.retry),
	)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open nclient4 on %s: %w", req.Bridge, err)
	}
	defer func() { _ = client.Close() }()

	// Without the broadcast flag, the server sends a unicast OFFER to the
	// generated chaddr MAC. The physical NIC drops it in hardware (not its MAC),
	// so the AF_PACKET socket on the bridge never sees the frame. Setting the
	// broadcast flag forces the server to respond with ff:ff:ff:ff:ff:ff, which
	// all NICs accept unconditionally.
	mods := append(identityModifiers(req.ClientID, req.Hostname, req.VendorClass), dhcpv4.WithBroadcast(true))
	lease, err := client.Request(ctx, mods...)
	if err != nil {
		return nil, fmt.Errorf("dhcp DORA on %s (client=%s): %w", req.Bridge, req.ClientID, err)
	}
	return leaseFromNClient4(req, lease), nil
}

func (c *NClient4Client) Renew(ctx context.Context, lease *Lease) (*Lease, error) {
	if lease == nil {
		return nil, fmt.Errorf("dhcp renew: lease is nil")
	}
	nclient4Lease, err := reconstructNClient4Lease(lease)
	if err != nil {
		return nil, fmt.Errorf("dhcp renew: %w", err)
	}

	conn, ifaceMAC, err := openPromiscBridgeConn(lease.Bridge)
	if err != nil {
		return nil, fmt.Errorf("open promisc conn on %s for renew: %w", lease.Bridge, err)
	}
	client, err := nclient4.NewWithConn(conn, ifaceMAC,
		nclient4.WithHWAddr(lease.HWAddr),
		nclient4.WithTimeout(c.timeout),
		nclient4.WithRetry(c.retry),
	)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open nclient4 on %s for renew: %w", lease.Bridge, err)
	}
	defer func() { _ = client.Close() }()

	renewed, err := client.Renew(ctx, nclient4Lease,
		identityModifiers(lease.ClientID, lease.Hostname, lease.VendorClass)...)
	if err != nil {
		return nil, fmt.Errorf("dhcp renew on %s (client=%s): %w", lease.Bridge, lease.ClientID, err)
	}

	return leaseFromNClient4(AcquireRequest{
		Bridge:      lease.Bridge,
		ClientID:    lease.ClientID,
		Hostname:    lease.Hostname,
		VendorClass: lease.VendorClass,
		HWAddr:      lease.HWAddr,
	}, renewed), nil
}

func (c *NClient4Client) Release(ctx context.Context, lease *Lease) error {
	if lease == nil {
		return nil
	}
	nclient4Lease, err := reconstructNClient4Lease(lease)
	if err != nil {
		return fmt.Errorf("dhcp release: %w", err)
	}

	conn, ifaceMAC, err := openPromiscBridgeConn(lease.Bridge)
	if err != nil {
		return fmt.Errorf("open promisc conn on %s for release: %w", lease.Bridge, err)
	}
	client, err := nclient4.NewWithConn(conn, ifaceMAC,
		nclient4.WithHWAddr(lease.HWAddr),
		nclient4.WithTimeout(c.timeout),
	)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("open nclient4 on %s for release: %w", lease.Bridge, err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Release(nclient4Lease,
		dhcpv4.WithOption(dhcpv4.OptClientIdentifier([]byte(lease.ClientID)))); err != nil {
		return fmt.Errorf("dhcp release on %s (client=%s): %w", lease.Bridge, lease.ClientID, err)
	}
	return nil
}

// identityModifiers builds the three identifying DHCP options we set on
// every outbound message: option 61 (client-id), option 12 (hostname),
// option 60 (vendor class).
func identityModifiers(clientID, hostname, vendorClass string) []dhcpv4.Modifier {
	var mods []dhcpv4.Modifier
	if clientID != "" {
		mods = append(mods, dhcpv4.WithOption(dhcpv4.OptClientIdentifier([]byte(clientID))))
	}
	if hostname != "" {
		mods = append(mods, dhcpv4.WithOption(dhcpv4.OptHostName(hostname)))
	}
	if vendorClass != "" {
		mods = append(mods, dhcpv4.WithOption(dhcpv4.OptClassIdentifier(vendorClass)))
	}
	return mods
}

func leaseFromNClient4(req AcquireRequest, in *nclient4.Lease) *Lease {
	ack := in.ACK
	leaseTime := ack.IPAddressLeaseTime(24 * time.Hour)
	return &Lease{
		Bridge:        req.Bridge,
		ClientID:      req.ClientID,
		Hostname:      req.Hostname,
		VendorClass:   req.VendorClass,
		HWAddr:        req.HWAddr,
		IP:            ack.YourIPAddr,
		SubnetMask:    ack.SubnetMask(),
		Routers:       ack.Router(),
		DNS:           ack.DNS(),
		ServerID:      ack.ServerIdentifier(),
		AcquiredAt:    in.CreationTime,
		LeaseDuration: leaseTime,
		T1:            ack.IPAddressRenewalTime(leaseTime / 2),
		T2:            ack.IPAddressRebindingTime(leaseTime * 7 / 8),
		RawOffer:      in.Offer.ToBytes(),
		RawACK:        ack.ToBytes(),
	}
}

func reconstructNClient4Lease(l *Lease) (*nclient4.Lease, error) {
	if len(l.RawOffer) == 0 || len(l.RawACK) == 0 {
		return nil, fmt.Errorf("lease is missing raw offer/ack bytes; renewal/release not possible")
	}
	offer, err := dhcpv4.FromBytes(l.RawOffer)
	if err != nil {
		return nil, fmt.Errorf("parse stored offer: %w", err)
	}
	ack, err := dhcpv4.FromBytes(l.RawACK)
	if err != nil {
		return nil, fmt.Errorf("parse stored ack: %w", err)
	}
	return &nclient4.Lease{
		Offer:        offer,
		ACK:          ack,
		CreationTime: l.AcquiredAt,
	}, nil
}
