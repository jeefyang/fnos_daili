package proxyconfig

import (
	"strings"
	"testing"
)

const base = `{"schema":1,"appname":"proxy.home.9f3a","mode":"reverse","target":"192.168.1.10:8123"}`

func TestParseDefaults(t *testing.T) {
	c, err := Parse([]byte(base))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if c.Target != "http://192.168.1.10:8123" {
		t.Fatalf("Target = %q", c.Target)
	}
	if c.Timeouts.Dial != "10s" || c.Timeouts.TLSHandshake != "10s" || c.Timeouts.ResponseHeader != "30s" || c.Timeouts.Idle != "90s" {
		t.Fatalf("timeouts = %+v", c.Timeouts)
	}
	if c.Direct() {
		t.Fatal("reverse mode reported direct")
	}
}

func TestParseReverseAuth(t *testing.T) {
	hash := "$2a$12$abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0"
	raw := `{"schema":1,"appname":"proxy.home.9f3a","mode":"reverse","target":"https://example.com","tls":{"skip_verify":true},"guard":{"allow_loopback":true},"basic_auth":{"username":"home","password_hash":"` + hash + `"},"timeouts":{"dial":"1s","tls_handshake":"2s","response_header":"3s","idle":"4s"}}`
	c, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if !c.TLS.SkipVerify || !c.Guard.AllowLoopback || c.BasicAuth == nil {
		t.Fatalf("unexpected config: %+v", c)
	}
	if c.Timeouts.Dial != "1s" || c.Timeouts.Idle != "4s" {
		t.Fatalf("timeouts = %+v", c.Timeouts)
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"bad json", "{"},
		{"wrong schema", strings.Replace(base, `"schema":1`, `"schema":2`, 1)},
		{"bad appname", strings.Replace(base, "proxy.home.9f3a", "-bad", 1)},
		{"manager appname", strings.Replace(base, "proxy.home.9f3a", "daili.manager", 1)},
		{"bad mode", strings.Replace(base, `"reverse"`, `"forward"`, 1)},
		{"bad target", strings.Replace(base, "192.168.1.10:8123", "http://host/path", 1)},
		{"bad dns", strings.Replace(base, `"reverse"`, `"reverse","dns":"223.5.5.5:53"`, 1)},
		{"bad timeout", strings.Replace(base, `"reverse"`, `"reverse","timeouts":{"dial":"soon"}`, 1)},
		{"zero timeout", strings.Replace(base, `"reverse"`, `"reverse","timeouts":{"dial":"0s"}`, 1)},
		{"direct auth", strings.Replace(base, `"reverse"`, `"direct","basic_auth":{"username":"home","password_hash":"$2a$12$abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0"}`, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if c, err := Parse([]byte(tt.raw)); err == nil {
				t.Fatalf("Parse() = %+v, want error", c)
			}
		})
	}
}

func TestParseDirectIgnoresTLSAndGuard(t *testing.T) {
	raw := strings.Replace(base, `"reverse"`, `"direct","tls":{"skip_verify":true},"guard":{"allow_loopback":true}`, 1)
	c, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if !c.Direct() {
		t.Fatal("mode is not direct")
	}
}
