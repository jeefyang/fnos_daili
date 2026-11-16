package spec

import (
	"strings"
	"testing"
)

func validSpec() ProxySpec {
	return ProxySpec{
		AppName:     "proxy.home.9f3a",
		DisplayName: "家里的 Home Assistant",
		Description: "通过代理访问 192.168.1.10:8123",
		TargetURL:   "192.168.1.10:8123",
		Mode:        "reverse",
	}
}

func TestNormalizeProxySpecDefaults(t *testing.T) {
	got, err := NormalizeProxySpec(validSpec())
	if err != nil {
		t.Fatalf("NormalizeProxySpec() error: %v", err)
	}
	if got.TargetURL != "http://192.168.1.10:8123" {
		t.Fatalf("TargetURL = %q", got.TargetURL)
	}
	if got.Version != "1.0.0" {
		t.Fatalf("Version = %q, want 1.0.0", got.Version)
	}
	if got.UIType != "url" {
		t.Fatalf("UIType = %q, want url", got.UIType)
	}
	if got.EntryTitle != got.DisplayName {
		t.Fatalf("EntryTitle = %q, want %q", got.EntryTitle, got.DisplayName)
	}
}

func TestNormalizeProxySpecAcceptsIframeAndAuth(t *testing.T) {
	in := validSpec()
	in.UIType = "iframe"
	in.BasicAuth = &BasicAuth{Username: "home", PasswordHash: "$2a$12$abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0"}
	got, err := NormalizeProxySpec(in)
	if err != nil {
		t.Fatalf("NormalizeProxySpec() error: %v", err)
	}
	if got.UIType != "iframe" || got.BasicAuth == nil {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestNormalizeProxySpecRejectsInvalid(t *testing.T) {
	tests := []struct {
		name  string
		mut   func(*ProxySpec)
		field string
	}{
		{"missing appname", func(s *ProxySpec) { s.AppName = "" }, "appname"},
		{"short appname", func(s *ProxySpec) { s.AppName = "ab" }, "appname"},
		{"long appname", func(s *ProxySpec) { s.AppName = strings.Repeat("a", 33) }, "appname"},
		{"bad appname start", func(s *ProxySpec) { s.AppName = "-bad.name" }, "appname"},
		{"reserved appname", func(s *ProxySpec) { s.AppName = ManagerAppName }, "appname"},
		{"missing display name", func(s *ProxySpec) { s.DisplayName = "" }, "display_name"},
		{"display newline", func(s *ProxySpec) { s.DisplayName = "a\nb" }, "display_name"},
		{"display too long", func(s *ProxySpec) { s.DisplayName = strings.Repeat("家", 33) }, "display_name"},
		{"desc newline", func(s *ProxySpec) { s.Description = "a\r\nb" }, "description"},
		{"desc too long", func(s *ProxySpec) { s.Description = strings.Repeat("家", 121) }, "description"},
		{"bad version", func(s *ProxySpec) { s.Version = "1.0" }, "version"},
		{"bad mode", func(s *ProxySpec) { s.Mode = "forward" }, "mode"},
		{"bad ui type", func(s *ProxySpec) { s.UIType = "desktop" }, "ui_type"},
		{"entry title too long", func(s *ProxySpec) { s.EntryTitle = strings.Repeat("家", 33) }, "entry_title"},
		{"bad volume", func(s *ProxySpec) { s.InstallVolume = "a/b" }, "install_volume"},
		{"bad target", func(s *ProxySpec) { s.TargetURL = "http://host/path" }, "target_url"},
		{"direct with auth", func(s *ProxySpec) {
			s.Mode = "direct"
			s.BasicAuth = &BasicAuth{Username: "home", PasswordHash: "$2a$12$abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0"}
		}, "basic_auth"},
		{"auth without user", func(s *ProxySpec) {
			s.BasicAuth = &BasicAuth{PasswordHash: "$2a$12$abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0"}
		}, "basic_auth"},
		{"auth without hash", func(s *ProxySpec) {
			s.BasicAuth = &BasicAuth{Username: "home"}
		}, "basic_auth"},
		{"auth bad hash", func(s *ProxySpec) {
			s.BasicAuth = &BasicAuth{Username: "home", PasswordHash: "plaintext"}
		}, "basic_auth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validSpec()
			tt.mut(&in)
			_, err := NormalizeProxySpec(in)
			if err == nil {
				t.Fatalf("NormalizeProxySpec() succeeded, want error for %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("error %q does not mention %q", err.Error(), tt.field)
			}
		})
	}
}
