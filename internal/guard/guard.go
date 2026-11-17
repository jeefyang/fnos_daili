package guard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type Guard struct {
	AllowLoopback bool
	Resolver      Resolver
	// Dialer is optional; a 10s dial timeout is used when nil.
	Dialer *net.Dialer
}

type Error struct {
	Reason string
	Addr   netip.Addr
}

func (e *Error) Error() string {
	if e.Addr.IsValid() {
		return fmt.Sprintf("target address rejected (%s): %s", e.Reason, e.Addr)
	}
	return "target address rejected (" + e.Reason + ")"
}

type ResolveError struct {
	Host string
	Err  error
}

func (e *ResolveError) Error() string { return "resolve " + e.Host + ": " + e.Err.Error() }
func (e *ResolveError) Unwrap() error { return e.Err }

var (
	ipv4Unspecified = netip.MustParsePrefix("0.0.0.0/8")
	ipv4LinkLocal   = netip.MustParsePrefix("169.254.0.0/16")
	ipv4Multicast   = netip.MustParsePrefix("224.0.0.0/4")
	ipv4Reserved    = netip.MustParsePrefix("240.0.0.0/4")
	ipv6LinkLocal   = netip.MustParsePrefix("fe80::/10")
	ipv6Multicast   = netip.MustParsePrefix("ff00::/8")
)

func (g *Guard) CheckHost(ctx context.Context, host string, port int) error {
	if err := checkPort(port); err != nil {
		return err
	}
	addrs, err := g.Resolve(ctx, host)
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		if err := g.CheckAddr(addr, port); err != nil {
			return err
		}
	}
	return nil
}

// Resolve turns host into checked-in-later addresses. IP literals pass
// through; names use the guard's resolver or the system one.
func (g *Guard) Resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr}, nil
	}
	resolver := g.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, &ResolveError{Host: host, Err: err}
	}
	if len(addrs) == 0 {
		return nil, &ResolveError{Host: host, Err: errors.New("no addresses")}
	}
	return addrs, nil
}

func (g *Guard) CheckAddr(addr netip.Addr, port int) error {
	if err := checkPort(port); err != nil {
		return err
	}
	if !addr.IsValid() {
		return &Error{Reason: "invalid"}
	}
	addr = addr.Unmap()
	if addr.Is4() {
		switch {
		case ipv4Unspecified.Contains(addr):
			return &Error{Reason: "unspecified", Addr: addr}
		case addr.IsLoopback():
			if g.AllowLoopback {
				return nil
			}
			return &Error{Reason: "loopback", Addr: addr}
		case ipv4LinkLocal.Contains(addr):
			return &Error{Reason: "linklocal", Addr: addr}
		case ipv4Multicast.Contains(addr):
			return &Error{Reason: "multicast", Addr: addr}
		case ipv4Reserved.Contains(addr):
			return &Error{Reason: "reserved", Addr: addr}
		}
		return nil
	}
	switch {
	case addr.IsUnspecified():
		return &Error{Reason: "unspecified", Addr: addr}
	case addr.IsLoopback():
		if g.AllowLoopback {
			return nil
		}
		return &Error{Reason: "loopback", Addr: addr}
	case ipv6LinkLocal.Contains(addr):
		return &Error{Reason: "linklocal", Addr: addr}
	case ipv6Multicast.Contains(addr):
		return &Error{Reason: "multicast", Addr: addr}
	}
	return nil
}

// DialContext resolves address through the guard, rejects every blocked
// address, then dials the first usable IP. Host names never reach the kernel
// resolver, so a pinned DNS server is honoured for outbound dials too.
func (g *Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, err
	}
	addrs, err := g.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := g.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	}
	var lastErr error
	for _, addr := range addrs {
		if err := g.CheckAddr(addr, port); err != nil {
			return nil, err
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), portText))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no addresses for %s", host)
	}
	return nil, lastErr
}

func checkPort(port int) error {
	if port < 1 || port > 65535 || (port >= 20000 && port <= 29999) || port == 5666 || port == 5667 {
		return &Error{Reason: "port"}
	}
	return nil
}
