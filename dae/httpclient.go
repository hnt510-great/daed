package dae

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/daeuniverse/dae/common/consts"
	"github.com/daeuniverse/dae/control"
	"github.com/daeuniverse/outbound/netproxy"
)

var HttpTransport = &http.Transport{
	DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialViaControlPlane(ctx, consts.OutboundControlPlaneRouting, addr)
	},
	TLSHandshakeTimeout:   10 * time.Second,
	DisableKeepAlives:     true,
	DisableCompression:    false,
	MaxIdleConns:          100,
	IdleConnTimeout:       90 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
	ForceAttemptHTTP2:     true,
}

func dialViaControlPlane(ctx context.Context, outbound consts.OutboundIndex, addr string) (net.Conn, error) {
	host, _port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no dns record: %v", host)
	}
	port, err := strconv.ParseUint(_port, 10, 16)
	if err != nil {
		return nil, err
	}
	ctl, err := ControlPlane()
	if err != nil {
		return nil, err
	}
	conn, err := ctl.RouteDialTcpContext(ctx, &control.RouteDialParam{
		Outbound:    outbound,
		Domain:      host,
		Mac:         [6]uint8{},
		ProcessName: [16]uint8{},
		Src:         netip.MustParseAddrPort("0.0.0.0:0"),
		Dest:        netip.AddrPortFrom(addrs[0], uint16(port)),
		Mark:        0,
	})
	if err != nil {
		return nil, err
	}
	return &netproxy.FakeNetConn{Conn: conn, LAddr: nil, RAddr: nil}, nil
}

// GroupHttpTransport returns an http.RoundTripper that forces TCP connections
// through a specific active outbound group in the running control plane.
// DNS resolution uses the system resolver while the TCP connection is dialed
// explicitly through the named group's outbound ID.
func GroupHttpTransport(groupName string) (*http.Transport, error) {
	ctl, err := ControlPlane()
	if err != nil {
		return nil, fmt.Errorf("control plane is not running: %w", err)
	}
	groups := ctl.OutboundGroups()
	var foundOutbound consts.OutboundIndex
	found := false
	for idx, g := range groups {
		if g != nil && g.Name == groupName {
			foundOutbound = consts.OutboundIndex(idx)
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("outbound group %q is not active in current control plane", groupName)
	}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialViaControlPlane(ctx, foundOutbound, addr)
		},
		TLSHandshakeTimeout:   10 * time.Second,
		DisableKeepAlives:     true,
		DisableCompression:    false,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}, nil
}
