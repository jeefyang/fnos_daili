package guard

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"testing"
)

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if addrs, ok := f[host]; ok {
		return addrs, nil
	}
	return nil, errors.New("not found")
}

func TestCheckIP(t *testing.T) {
	tests := []struct {
		ip     string
		allow  bool
		reason string
	}{
		{"10.0.0.1", false, ""},
		{"192.168.1.1", false, ""},
		{"172.16.0.1", false, ""},
		{"8.8.8.8", false, ""},
		{"fc00::1", false, ""},
		{"2001:db8::1", false, ""},
		{"127.0.0.1", false, "loopback"},
		{"127.0.0.1", true, ""},
		{"::1", false, "loopback"},
		{"::1", true, ""},
		{"::ffff:127.0.0.1", false, "loopback"},
		{"::ffff:10.0.0.1", false, ""},
		{"0.0.0.0", false, "unspecified"},
		{"0.1.2.3", false, "unspecified"},
		{"169.254.1.1", false, "linklocal"},
		{"fe80::1", false, "linklocal"},
		{"224.0.0.1", false, "multicast"},
		{"ff02::1", false, "multicast"},
		{"240.0.0.1", false, "reserved"},
		{"255.255.255.255", false, "reserved"},
		{"::", false, "unspecified"},
	}
	for _, tt := range tests {
		t.Run(tt.ip+"/"+map[bool]string{true: "allow", false: "deny"}[tt.allow], func(t *testing.T) {
			g := &Guard{AllowLoopback: tt.allow}
			err := g.CheckAddr(netip.MustParseAddr(tt.ip), 8080)
			if tt.reason == "" {
				if err != nil {
					t.Fatalf("CheckAddr(%s) error: %v", tt.ip, err)
				}
				return
			}
			var ge *Error
			if !errors.As(err, &ge) || ge.Reason != tt.reason {
				t.Fatalf("CheckAddr(%s) = %v, want reason %q", tt.ip, err, tt.reason)
			}
		})
	}
}

func TestCheckPort(t *testing.T) {
	g := &Guard{}
	for _, port := range []int{20000, 25000, 29999, 5666, 5667} {
		if err := g.CheckAddr(netip.MustParseAddr("10.0.0.1"), port); err == nil {
			t.Fatalf("port %d accepted", port)
		}
	}
	for _, port := range []int{80, 443, 19999, 30000, 8080} {
		if err := g.CheckAddr(netip.MustParseAddr("10.0.0.1"), port); err != nil {
			t.Fatalf("port %d rejected: %v", port, err)
		}
	}
}

func TestCheckHostResolvesAll(t *testing.T) {
	g := &Guard{Resolver: fakeResolver{
		"ok.example":    {netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("8.8.8.8")},
		"mixed.example": {netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("127.0.0.1")},
	}}
	if err := g.CheckHost(context.Background(), "ok.example", 80); err != nil {
		t.Fatalf("ok.example rejected: %v", err)
	}
	err := g.CheckHost(context.Background(), "mixed.example", 80)
	var ge *Error
	if !errors.As(err, &ge) || ge.Reason != "loopback" {
		t.Fatalf("mixed.example = %v, want loopback", err)
	}
}

func TestCheckHostResolutionError(t *testing.T) {
	g := &Guard{Resolver: fakeResolver{}}
	err := g.CheckHost(context.Background(), "missing.example", 80)
	var re *ResolveError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want ResolveError", err)
	}
}

func TestCheckHostLiteral(t *testing.T) {
	g := &Guard{}
	if err := g.CheckHost(context.Background(), "[::1]", 80); err == nil {
		t.Fatal("bracketed loopback accepted")
	}
	if err := g.CheckHost(context.Background(), "10.0.0.1", 80); err != nil {
		t.Fatalf("literal rejected: %v", err)
	}
}

func TestDialContextBlocksLoopback(t *testing.T) {
	g := &Guard{}
	if _, err := g.DialContext(context.Background(), "tcp", "127.0.0.1:8080"); err == nil {
		t.Fatal("loopback dial accepted")
	} else {
		var ge *Error
		if !errors.As(err, &ge) || ge.Reason != "loopback" {
			t.Fatalf("err = %v, want loopback guard error", err)
		}
	}
}

func TestDialContextDialsResolvedAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, portText, _ := net.SplitHostPort(ln.Addr().String())
	if port, _ := strconv.Atoi(portText); port >= 20000 && port <= 29999 {
		t.Skipf("listener port %d is a blocked proxy port", port)
	}
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			conn.Close()
		}
	}()
	g := &Guard{AllowLoopback: true}
	conn, err := g.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close()
}

func TestDialContextResolveError(t *testing.T) {
	g := &Guard{Resolver: fakeResolver{}}
	_, err := g.DialContext(context.Background(), "tcp", "missing.example:80")
	var re *ResolveError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want ResolveError", err)
	}
}
