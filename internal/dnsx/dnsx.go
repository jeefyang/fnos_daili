// Package dnsx builds a resolver that talks to an explicit DNS server.
//
// Proxied targets sometimes need a resolver other than the one in
// /etc/resolv.conf: a NAS may point at an unreachable router stub, and a
// per-target override keeps the generated app self-contained.
package dnsx

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

// Validate reports whether server is an empty override or an IP literal.
func Validate(server string) error {
	s := strings.TrimSpace(server)
	if s == "" {
		return nil
	}
	if _, err := netip.ParseAddr(s); err != nil {
		return fmt.Errorf("DNS server must be an IP address; port 53 is implied")
	}
	return nil
}

// New returns a resolver that sends every query to server. The stub resolver
// talks UDP first and retries over TCP when an answer was truncated.
func New(server string) *net.Resolver {
	addr := net.JoinHostPort(strings.TrimSpace(server), "53")
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			if network != "tcp" {
				network = "udp"
			}
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, addr)
		},
	}
}
