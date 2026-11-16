package target

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"adds http scheme", "192.168.1.10:8123", "http://192.168.1.10:8123"},
		{"lowercases host and strips default https port", "https://Example.COM:443/", "https://example.com"},
		{"keeps non-default port", "HTTP://Example.COM:8080", "http://example.com:8080"},
		{"ipv6 literal", "http://[fd00::1]:8080/", "http://[fd00::1]:8080"},
		{"ipv6 default port", "https://[fd00::1]:443", "https://[fd00::1]"},
		{"strips trailing dot", "http://example.com.", "http://example.com"},
		{"trims whitespace", "  https://example.com  ", "https://example.com"},
		{"single label host", "http://nas:8080", "http://nas:8080"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(tt.in)
			if err != nil {
				t.Fatalf("Normalize(%q) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeRejectsNonOrigin(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"path", "http://host/path"},
		{"query", "http://host?q=1"},
		{"fragment", "http://host#x"},
		{"bare fragment", "http://host#"},
		{"userinfo", "http://user:pass@host"},
		{"unsupported scheme", "ftp://host"},
		{"empty host", "http://"},
		{"port zero", "http://host:0"},
		{"port too large", "http://host:65536"},
		{"non-numeric port", "http://host:abc"},
		{"underscore host", "http://bad_host"},
		{"unicode host", "http://例子.com"},
		{"invalid ipv4", "http://999.1.1.1"},
		{"zone", "http://[fe80::1%25eth0]"},
		{"empty", "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := Normalize(tt.in); err == nil {
				t.Fatalf("Normalize(%q) = %q, want error", tt.in, got)
			}
		})
	}
}

func TestNormalizeAcceptsIPv4AndIPv6(t *testing.T) {
	tests := map[string]string{
		"http://192.168.1.10":      "http://192.168.1.10",
		"http://[::1]":             "http://[::1]",
		"https://[::ffff:1.2.3.4]": "https://[::ffff:1.2.3.4]",
	}
	for in, want := range tests {
		got, err := Normalize(in)
		if err != nil {
			t.Fatalf("Normalize(%q) error: %v", in, err)
		}
		if got != want {
			t.Fatalf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
