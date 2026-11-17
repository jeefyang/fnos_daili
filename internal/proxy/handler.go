package proxy

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/jeefyang/fnos_daili/internal/dnsx"
	"github.com/jeefyang/fnos_daili/internal/guard"
	"github.com/jeefyang/fnos_daili/internal/proxyconfig"
	"golang.org/x/crypto/bcrypt"
)

const (
	privateClientIPHeader = "X-Daili-Client-IP"
	privateEntryProto     = "X-Daili-Entry-Proto"
)

type ctxKey int

const requestIDKey ctxKey = iota

type Options struct {
	Logger   *log.Logger
	Resolver guard.Resolver
	CGIDial  func(ctx context.Context, network, address string) (net.Conn, error)
	// Root serves the entry at the origin root (independent TCP port). The
	// default keeps the CGI prefix used by index.cgi entries.
	Root bool
}

type Handler struct {
	cfg      *proxyconfig.Config
	target   *url.URL
	prefix   string
	guard    *guard.Guard
	resolver guard.Resolver
	proxy    *httputil.ReverseProxy
	logger   *log.Logger
}

func New(cfg *proxyconfig.Config, opts Options) (*Handler, error) {
	if cfg == nil {
		return nil, errors.New("nil config")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	target, err := cfg.TargetURL()
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	prefix := EntryPrefix(cfg.AppName)
	if opts.Root {
		prefix = ""
	}
	h := &Handler{
		cfg:    cfg,
		target: target,
		prefix: prefix,
		logger: logger,
	}
	if cfg.Direct() {
		return h, nil
	}
	h.resolver = opts.Resolver
	if h.resolver == nil && cfg.DNS != "" {
		h.resolver = dnsx.New(cfg.DNS)
	}
	h.guard = &guard.Guard{AllowLoopback: cfg.Guard.AllowLoopback, Resolver: h.resolver}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = h.guard.CheckHost(ctx, target.Hostname(), targetPort(target))
	if err != nil {
		var re *guard.ResolveError
		if errors.As(err, &re) {
			logger.Printf("startup target %s: %v", target.Host, err)
		} else {
			return nil, fmt.Errorf("target rejected: %w", err)
		}
	}
	h.proxy = h.newReverseProxy()
	return h, nil
}

func EntryPrefix(appname string) string {
	return "/cgi/ThirdParty/" + appname + "/index.cgi"
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := newRequestID()
	w.Header().Set("X-Request-Id", id)
	r = r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
	rw := &responseWriter{ResponseWriter: w, status: 200}
	start := time.Now()
	defer func() {
		h.logger.Printf("%s %s %s %s %d %d %s", id, h.cfg.AppName, r.Method, r.URL.Path, rw.status, rw.bytes, time.Since(start))
	}()

	if h.cfg.BasicAuth != nil {
		user, pass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(h.cfg.BasicAuth.Username)) == 1
		passErr := bcrypt.CompareHashAndPassword([]byte(h.cfg.BasicAuth.PasswordHash), []byte(pass))
		if !ok || !userOK || passErr != nil {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm="%s"`, h.cfg.AppName))
			writeError(w, id, 401, "Authentication required")
			return
		}
		r.Header.Del("Authorization")
		r.Header.Del("Proxy-Authorization")
	}
	if h.cfg.Direct() {
		h.redirect(rw, r)
		return
	}
	h.proxy.ServeHTTP(rw, r)
}

func (h *Handler) redirect(w http.ResponseWriter, r *http.Request) {
	clean := path.Clean("/" + r.URL.Path)
	if strings.HasPrefix(clean, "//") {
		clean = "/"
	}
	location := h.target.Scheme + "://" + h.target.Host + clean
	if r.URL.RawQuery != "" {
		location += "?" + r.URL.RawQuery
	}
	w.Header().Set("Location", location)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusFound)
}

func (h *Handler) newReverseProxy() *httputil.ReverseProxy {
	transport := h.newTransport()
	return &httputil.ReverseProxy{
		Rewrite:        h.rewrite,
		Transport:      transport,
		FlushInterval:  -1,
		ModifyResponse: h.modifyResponse,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			h.logger.Printf("%s upstream error: %v", requestID(r), err)
			writeError(w, requestID(r), http.StatusBadGateway, "Upstream request failed")
		},
		ErrorLog: log.New(io.Discard, "", 0),
	}
}

func (h *Handler) rewrite(pr *httputil.ProxyRequest) {
	pr.Out.URL.Scheme = h.target.Scheme
	pr.Out.URL.Host = h.target.Host
	pr.Out.URL.User = nil
	pr.Out.Host = h.target.Host

	pr.Out.Header.Del("Forwarded")
	for _, key := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		pr.Out.Header.Del(key)
	}
	removeHopHeaders(pr.Out.Header)
	pr.Out.Header.Del(privateClientIPHeader)
	pr.Out.Header.Del(privateEntryProto)

	client := clientIP(pr.In)
	proto := entryProto(pr.In)
	pr.Out.Header.Set("X-Forwarded-For", client)
	pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
	pr.Out.Header.Set("X-Forwarded-Proto", proto)

	entryOrigin := proto + "://" + pr.In.Host
	targetOrigin := h.target.Scheme + "://" + h.target.Host
	if origin := pr.Out.Header.Get("Origin"); origin != "" && sameOrigin(origin, entryOrigin) {
		pr.Out.Header.Set("Origin", targetOrigin)
	}
	if referer := pr.Out.Header.Get("Referer"); referer != "" {
		if u, err := url.Parse(referer); err == nil && u.Scheme != "" && u.Host != "" && strings.EqualFold(u.Scheme, proto) && strings.EqualFold(u.Host, pr.In.Host) {
			u.Scheme = h.target.Scheme
			u.Host = h.target.Host
			pr.Out.Header.Set("Referer", u.String())
		}
	}
}

func (h *Handler) newTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: h.cfg.Timeouts.DialDuration(), KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                 nil,
		DialContext:           h.guardDial(dialer),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       h.cfg.Timeouts.IdleDuration(),
		TLSHandshakeTimeout:   h.cfg.Timeouts.TLSHandshakeDuration(),
		ResponseHeaderTimeout: h.cfg.Timeouts.ResponseHeaderDuration(),
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: h.cfg.TLS.SkipVerify}, // #nosec G402 - explicit per-app opt-in.
	}
}

func (h *Handler) guardDial(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	h.guard.Dialer = dialer
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := h.guard.DialContext(ctx, network, address)
		if err != nil {
			var re *guard.ResolveError
			if errors.As(err, &re) {
				h.logger.Printf("%s target resolve failed: %v", requestIDFrom(ctx), err)
			} else {
				h.logger.Printf("%s target dial failed: %v", requestIDFrom(ctx), err)
			}
		}
		return conn, err
	}
}

func (h *Handler) modifyResponse(resp *http.Response) error {
	if resp.Request != nil {
		if id := requestID(resp.Request); id != "" {
			resp.Header.Set("X-Request-Id", id)
		}
	}
	h.rewriteLocation(resp)
	h.rewriteCookies(resp)
	for _, key := range []string{
		"Strict-Transport-Security",
		"Alt-Svc",
		"Public-Key-Pins",
		"Public-Key-Pins-Report-Only",
	} {
		resp.Header.Del(key)
	}
	return nil
}

func (h *Handler) rewriteLocation(resp *http.Response) {
	location := resp.Header.Get("Location")
	if location == "" {
		return
	}
	u, err := url.Parse(location)
	if err != nil {
		return
	}
	if u.Scheme == "" && u.Host == "" {
		return
	}
	if !strings.EqualFold(u.Host, h.target.Host) {
		return
	}
	if u.Scheme != "" && !strings.EqualFold(u.Scheme, h.target.Scheme) {
		return
	}
	clean := u.Path
	if clean == "" {
		clean = "/"
	}
	u.Scheme = ""
	u.Host = ""
	u.User = nil
	u.Path = h.prefix + clean
	u.RawPath = ""
	resp.Header.Set("Location", u.String())
}

func (h *Handler) rewriteCookies(resp *http.Response) {
	values := resp.Header.Values("Set-Cookie")
	if len(values) == 0 {
		return
	}
	resp.Header.Del("Set-Cookie")
	for _, raw := range values {
		cookie, err := http.ParseSetCookie(raw)
		if err != nil {
			resp.Header.Add("Set-Cookie", raw)
			continue
		}
		cookie.Domain = ""
		cookie.Path = h.cookiePath(cookie.Path)
		resp.Header.Add("Set-Cookie", cookie.String())
	}
}

func (h *Handler) cookiePath(p string) string {
	if p == "" || p == "/" {
		return h.prefix + "/"
	}
	if p == h.prefix || strings.HasPrefix(p, h.prefix+"/") {
		return p
	}
	if strings.HasPrefix(p, "/") {
		return h.prefix + p
	}
	return h.prefix + "/"
}

func removeHopHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if token = strings.TrimSpace(token); token != "" {
				header.Del(token)
			}
		}
	}
	for _, key := range []string{
		"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate",
		"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		header.Del(key)
	}
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get(privateClientIPHeader); v != "" {
		return v
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func entryProto(r *http.Request) string {
	if v := r.Header.Get(privateEntryProto); v == "http" || v == "https" {
		return v
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func targetPort(u *url.URL) int {
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			return n
		}
	}
	if strings.EqualFold(u.Scheme, "https") {
		return 443
	}
	return 80
}

func sameOrigin(raw, origin string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	o, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Scheme, o.Scheme) && strings.EqualFold(u.Host, o.Host)
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func requestID(r *http.Request) string { return requestIDFrom(r.Context()) }

func requestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

func writeError(w http.ResponseWriter, id string, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Request-Id", id)
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<!doctype html><html><head><meta charset=\"utf-8\"><title>%d</title></head><body><h1>%d</h1><p>%s</p><p>X-Request-Id: %s</p></body></html>", status, status, html.EscapeString(message), html.EscapeString(id))
}

type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
	wrote  bool
}

func (w *responseWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking not supported")
	}
	return h.Hijack()
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
