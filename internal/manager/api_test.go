package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jeefyang/fnos_daili/internal/state"
)

func TestAPIGetTargetsSetsCSRFCookie(t *testing.T) {
	s, _ := newTestService(t)
	h := s.Handler(http.FileServerFS(UI()))
	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/targets", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "daili_csrf" && c.Value == s.Token() && c.HttpOnly {
			found = true
		}
	}
	if !found {
		t.Fatalf("cookies = %v", rec.Result().Cookies())
	}
}

func TestAPIPostRequiresCSRFAndSameOrigin(t *testing.T) {
	s, _ := newTestService(t)
	h := s.Handler(http.FileServerFS(UI()))
	body := `{"appname":"daili.demo","display_name":"Demo","target_url":"http://192.168.1.10:8123","mode":"reverse"}`
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/targets", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	req = httptest.NewRequest(http.MethodPost, "http://example.com/api/targets", strings.NewReader(body))
	req.Header.Set("X-CSRF-Token", s.Token())
	req.Header.Set("Origin", "http://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d body = %s", rec.Code, rec.Body)
	}
	req = httptest.NewRequest(http.MethodPost, "http://example.com/api/targets", strings.NewReader(body))
	req.Header.Set("X-CSRF-Token", s.Token())
	req.Header.Set("Origin", "http://example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	var got TargetView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.AppName != "daili.demo" || got.Runtime.State != "draft" {
		t.Fatalf("target = %+v", got)
	}
}

func TestAPIPostAcceptsForwardedHost(t *testing.T) {
	s, _ := newTestService(t)
	h := s.Handler(http.FileServerFS(UI()))
	body := `{"appname":"daili.demo","display_name":"Demo","target_url":"http://192.168.1.10:8123","mode":"reverse"}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000/api/targets", strings.NewReader(body))
	req.Header.Set("X-CSRF-Token", s.Token())
	req.Header.Set("Origin", "https://jeefyang.fnos.net")
	req.Header.Set("X-Forwarded-Host", "jeefyang.fnos.net, internal")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}

func TestSameOriginIgnoresDefaultPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://example.com:443/api/targets", nil)
	req.Header.Set("Origin", "https://example.com")
	if !sameOrigin(req) {
		t.Fatal("sameOrigin rejected matching host with different port")
	}
}

func TestAPIPostAllowsLoopbackCGIUpstream(t *testing.T) {
	s, _ := newTestService(t)
	h := s.Handler(http.FileServerFS(UI()))
	body := `{"appname":"daili.demo","display_name":"Demo","target_url":"http://192.168.1.10:8123","mode":"reverse"}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9000/api/targets", strings.NewReader(body))
	req.Header.Set("X-CSRF-Token", s.Token())
	req.Header.Set("Origin", "https://public.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}

func TestAPISettingsRoundTrip(t *testing.T) {
	s, _ := newTestService(t)
	h := s.Handler(http.FileServerFS(UI()))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/settings", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d body = %s", rec.Code, rec.Body)
	}
	var got state.Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AppNamePrefix != "proxy." || got.AppNameSuffix != "" {
		t.Fatalf("default settings = %+v", got)
	}

	body := `{"appname_prefix":"-bad","appname_suffix":""}`
	req = httptest.NewRequest(http.MethodPut, "http://example.com/api/settings", strings.NewReader(body))
	req.Header.Set("X-CSRF-Token", s.Token())
	req.Header.Set("Origin", "http://example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid PUT status = %d body = %s", rec.Code, rec.Body)
	}

	body = `{"appname_prefix":"daili.","appname_suffix":".prod"}`
	req = httptest.NewRequest(http.MethodPut, "http://example.com/api/settings", strings.NewReader(body))
	req.Header.Set("X-CSRF-Token", s.Token())
	req.Header.Set("Origin", "http://example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid PUT status = %d body = %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AppNamePrefix != "daili." || got.AppNameSuffix != ".prod" {
		t.Fatalf("saved settings = %+v", got)
	}
	if s.Settings().AppNamePrefix != "daili." {
		t.Fatalf("service settings = %+v", s.Settings())
	}
}

func TestAPINetworkReportsLAN(t *testing.T) {
	s, _ := newTestService(t)
	h := s.Handler(http.FileServerFS(UI()))

	cases := []struct {
		name string
		ip   string
		want bool
	}{
		{"private", "192.168.1.20", true},
		{"loopback", "127.0.0.1", true},
		{"public", "203.0.113.9", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example.com/api/network", nil)
			req.Header.Set("X-Daili-Client-IP", tc.ip)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
			}
			var got struct {
				ClientIP string `json:"client_ip"`
				LAN      *bool  `json:"lan"`
				Known    bool   `json:"known"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !got.Known || got.LAN == nil || *got.LAN != tc.want {
				t.Fatalf("network = %+v", got)
			}
		})
	}
}

func TestIndexInjectsCSRFToken(t *testing.T) {
	s, _ := newTestService(t)
	h := s.Handler(http.FileServerFS(UI()))
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), s.Token()) {
		t.Fatal("index does not contain the CSRF token")
	}
}
