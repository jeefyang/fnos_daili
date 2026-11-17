package proxy

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jeefyang/fnos_daili/internal/proxyconfig"
	"github.com/jeefyang/fnos_daili/internal/spec"
	"golang.org/x/crypto/bcrypt"
)

const entryPrefix = "/cgi/ThirdParty/proxy.test/index.cgi"

func reverseConfig(t *testing.T, targetURL string, allowLoopback bool) *proxyconfig.Config {
	t.Helper()
	c := &proxyconfig.Config{
		Schema:  1,
		AppName: "proxy.test",
		Mode:    "reverse",
		Target:  targetURL,
		Guard:   proxyconfig.Guard{AllowLoopback: allowLoopback},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
	return c
}

func newProxy(t *testing.T, c *proxyconfig.Config) *httptest.Server {
	t.Helper()
	h, err := New(c, Options{})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func get(t *testing.T, s *httptest.Server, path string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", s.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := s.Client().Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestReverseRewriteAndResponse(t *testing.T) {
	type seen struct {
		host, xff, xfh, xfp, origin, referer string
		hop, te, upgrade                     string
	}
	got := make(chan seen, 1)
	var targetURL string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- seen{
			host: r.Host, xff: r.Header.Get("X-Forwarded-For"),
			xfh: r.Header.Get("X-Forwarded-Host"), xfp: r.Header.Get("X-Forwarded-Proto"),
			origin: r.Header.Get("Origin"), referer: r.Header.Get("Referer"),
			hop: r.Header.Get("X-Hop"), te: r.Header.Get("Te"), upgrade: r.Header.Get("Upgrade"),
		}
		w.Header().Set("Location", targetURL+"/next?x=1")
		w.Header().Add("Set-Cookie", "sid=abc; Domain=example.com; Path=/app; Secure; HttpOnly; SameSite=Lax")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		w.Header().Set("Alt-Svc", "h3=\":443\"")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()
	targetURL = target.URL

	s := newProxy(t, reverseConfig(t, target.URL, true))
	resp := get(t, s, "/foo?q=1", map[string]string{
		"X-Forwarded-For":   "1.2.3.4",
		"X-Forwarded-Host":  "attacker",
		"X-Forwarded-Proto": "https",
		"X-Hop":             "1",
		"Connection":        "X-Hop",
		"Te":                "trailers",
		"Upgrade":           "websocket",
		"Origin":            s.URL,
		"Referer":           s.URL + "/page",
	})
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
	if resp.Header.Get("Location") != entryPrefix+"/next?x=1" {
		t.Fatalf("Location = %q", resp.Header.Get("Location"))
	}
	cookie := resp.Header.Get("Set-Cookie")
	if !strings.Contains(cookie, "Path="+entryPrefix+"/app") || strings.Contains(cookie, "Domain=") || !strings.Contains(cookie, "Secure") {
		t.Fatalf("Set-Cookie = %q", cookie)
	}
	if resp.Header.Get("Strict-Transport-Security") != "" || resp.Header.Get("Alt-Svc") != "" {
		t.Fatalf("security headers not stripped: %v", resp.Header)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(resp.Header.Get("X-Request-Id")) {
		t.Fatalf("X-Request-Id = %q", resp.Header.Get("X-Request-Id"))
	}
	select {
	case v := <-got:
		if v.host != strings.TrimPrefix(target.URL, "http://") {
			t.Fatalf("target Host = %q", v.host)
		}
		if v.xff != "127.0.0.1" || v.xfh != strings.TrimPrefix(s.URL, "http://") || v.xfp != "http" {
			t.Fatalf("forwarded headers = %+v", v)
		}
		if v.origin != target.URL || v.referer != target.URL+"/page" {
			t.Fatalf("origin/referer = %+v", v)
		}
		if v.hop != "" || v.upgrade != "" {
			t.Fatalf("hop headers leaked: %+v", v)
		}
		// ReverseProxy re-adds "Te: trailers" on purpose so upstreams may use
		// trailers; that is standard proxy behaviour and not a leak.
		if v.te != "trailers" {
			t.Fatalf("te = %q", v.te)
		}
	default:
		t.Fatal("target not called")
	}
}

func TestRootMountRewritesWithoutPrefix(t *testing.T) {
	var targetURL string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", targetURL+"/next?x=1")
		w.Header().Add("Set-Cookie", "sid=abc; Domain=example.com; Path=/app")
		w.WriteHeader(200)
	}))
	defer target.Close()
	targetURL = target.URL

	c := reverseConfig(t, target.URL, true)
	h, err := New(c, Options{Root: true})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	defer s.Close()
	resp := get(t, s, "/foo", nil)
	if resp.Header.Get("Location") != "/next?x=1" {
		t.Fatalf("Location = %q", resp.Header.Get("Location"))
	}
	if cookie := resp.Header.Get("Set-Cookie"); !strings.Contains(cookie, "Path=/app") || strings.Contains(cookie, "Domain=") {
		t.Fatalf("Set-Cookie = %q", cookie)
	}
}

func TestReverseStartupRejectsLoopback(t *testing.T) {
	c := reverseConfig(t, "http://127.0.0.1:9", false)
	if _, err := New(c, Options{}); err == nil {
		t.Fatal("New() accepted loopback target")
	}
}

func TestReverseErrorPage(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	targetURL := target.URL
	target.Close()
	s := newProxy(t, reverseConfig(t, targetURL, true))
	resp := get(t, s, "/", nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 502 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "502") || !strings.Contains(string(body), "X-Request-Id") {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(string(body), targetURL) {
		t.Fatalf("body leaks target: %s", body)
	}
}

func TestDirectRedirect(t *testing.T) {
	c := &proxyconfig.Config{Schema: 1, AppName: "proxy.test", Mode: "direct", Target: "http://10.0.0.1:8080"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	h, err := New(c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "http://proxy/a/../b?x=1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 302 {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "http://10.0.0.1:8080/b?x=1" {
		t.Fatalf("Location = %q", got)
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Body.Len() != 0 {
		t.Fatalf("headers/body = %v %q", rec.Header(), rec.Body.String())
	}
}

func TestBasicAuth(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Errorf("authorization forwarded")
		}
		w.WriteHeader(200)
	}))
	defer target.Close()
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), 12)
	if err != nil {
		t.Fatal(err)
	}
	c := reverseConfig(t, target.URL, true)
	c.BasicAuth = &spec.BasicAuth{Username: "home", PasswordHash: string(hash)}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	s := newProxy(t, c)
	resp := get(t, s, "/", nil)
	if resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") != `Basic realm="proxy.test"` {
		t.Fatalf("status/auth = %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	req, _ := http.NewRequest("GET", s.URL+"/", nil)
	req.SetBasicAuth("home", "secret")
	resp2, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("authenticated status = %d", resp2.StatusCode)
	}
}

func TestCGIStripAndForward(t *testing.T) {
	type seen struct{ path, query, host, client, proto string }
	got := make(chan seen, 1)
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- seen{
			path: r.URL.Path, query: r.URL.RawQuery, host: r.Host,
			client: r.Header.Get("X-Daili-Client-IP"), proto: r.Header.Get("X-Daili-Entry-Proto"),
		}
		w.Header().Set("X-Inner", "1")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("inner"))
	}))
	defer inner.Close()
	addr := inner.Listener.Addr().String()
	cgiHandler, err := NewCGI("unused", Options{CGIDial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return net.Dial("tcp", addr)
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(cgiHandler)
	defer s.Close()

	req, _ := http.NewRequest("GET", s.URL+"/cgi/ThirdParty/proxy.test/index.cgi/hello?x=1", nil)
	req.Host = "fnos.local"
	resp, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "inner" || resp.Header.Get("X-Inner") != "1" {
		t.Fatalf("response = %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(resp.Header.Get("X-Request-Id")) {
		t.Fatalf("X-Request-Id = %q", resp.Header.Get("X-Request-Id"))
	}
	select {
	case v := <-got:
		if v.path != "/hello" || v.query != "x=1" || v.host != "fnos.local" || v.client != "127.0.0.1" || v.proto != "http" {
			t.Fatalf("inner saw %+v", v)
		}
	default:
		t.Fatal("inner not called")
	}
}

func TestReverseProxyUpgradesWebSocket(t *testing.T) {
	crlf := string([]byte{13, 10})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected upgrade", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, brw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		brw.WriteString("HTTP/1.1 101 Switching Protocols" + crlf + "Upgrade: websocket" + crlf + "Connection: Upgrade" + crlf + crlf)
		brw.Flush()
	}))
	defer target.Close()

	h, err := New(reverseConfig(t, target.URL, true), Options{Root: true})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	defer s.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(s.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	request := strings.Join([]string{
		"GET /api/websocket HTTP/1.1",
		"Host: " + strings.TrimPrefix(s.URL, "http://"),
		"Upgrade: websocket",
		"Connection: Upgrade",
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==",
		"Sec-WebSocket-Version: 13",
		"",
		"",
	}, crlf)
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString(byte(10))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "101") {
		t.Fatalf("status line = %q", strings.TrimSpace(line))
	}
}

func TestCGIRejectsWebSocketAndBadPath(t *testing.T) {
	cgiHandler, err := NewCGI("unused", Options{CGIDial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, io.EOF
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(cgiHandler)
	defer s.Close()

	req, _ := http.NewRequest("GET", s.URL+"/index.cgi/ws", nil)
	req.Header.Set("Connection", "keep-alive, Upgrade")
	req.Header.Set("Upgrade", "websocket")
	resp, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 501 {
		t.Fatalf("websocket status = %d", resp.StatusCode)
	}
	resp2 := get(t, s, "/not-cgi", nil)
	if resp2.StatusCode != 404 {
		t.Fatalf("bad path status = %d", resp2.StatusCode)
	}
}

func TestCGISocketFailure(t *testing.T) {
	cgiHandler, err := NewCGI("unused", Options{CGIDial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, io.EOF
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(cgiHandler)
	defer s.Close()
	resp := get(t, s, "/index.cgi/", nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 503 || !strings.Contains(string(body), "503") {
		t.Fatalf("status/body = %d %q", resp.StatusCode, body)
	}
}

func TestNewCGIEmptySocket(t *testing.T) {
	if _, err := NewCGI("", Options{}); err == nil {
		t.Fatal("NewCGI accepted empty socket")
	}
}

func TestSplitCGIPath(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		valid bool
	}{
		{"/cgi/ThirdParty/daili.manager/index.cgi", "/", true},
		{"/cgi/ThirdParty/daili.manager/index.cgi/", "/", true},
		{"/cgi/ThirdParty/daili.manager/index.cgi/api/targets", "/api/targets", true},
		{"/index.cgi/x", "/x", true},
		{"/index.cgi2/x", "", false},
		{"/not-cgi", "", false},
	}
	for _, c := range cases {
		got, ok := splitCGIPath(c.in)
		if got != c.want || ok != c.valid {
			t.Errorf("splitCGIPath(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.valid)
		}
	}
}
