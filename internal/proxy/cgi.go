package proxy

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"time"
)

var errEmptySocket = errors.New("empty socket path")

type cgiHandler struct {
	proxy  *httputil.ReverseProxy
	logger *log.Logger
}

func NewCGI(socket string, opts Options) (http.Handler, error) {
	if socket == "" {
		return nil, errEmptySocket
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	dial := opts.CGIDial
	if dial == nil {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		}
	}
	transport := &http.Transport{
		Proxy:              nil,
		DialContext:        func(ctx context.Context, network, address string) (net.Conn, error) { return dial(ctx, "unix", socket) },
		DisableCompression: true,
		MaxIdleConns:       4,
		IdleConnTimeout:    90 * time.Second,
	}
	h := &cgiHandler{logger: logger}
	h.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			clean := strings.TrimPrefix(pr.In.URL.Path, "/index.cgi")
			if clean == "" {
				clean = "/"
			}
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "unix"
			pr.Out.URL.Path = clean
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Set(privateClientIPHeader, clientIP(pr.In))
			pr.Out.Header.Set(privateEntryProto, cgiProto(pr.In))
		},
		Transport:     transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Printf("%s cgi upstream error: %v", requestID(r), err)
			writeError(w, requestID(r), http.StatusServiceUnavailable, "Service unavailable")
		},
		ErrorLog: log.New(io.Discard, "", 0),
	}
	return h, nil
}

func (h *cgiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := newRequestID()
	w.Header().Set("X-Request-Id", id)
	r = r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
	if r.URL.Path != "/index.cgi" && !strings.HasPrefix(r.URL.Path, "/index.cgi/") {
		writeError(w, id, http.StatusNotFound, "Not found")
		return
	}
	if isUpgrade(r) {
		writeError(w, id, http.StatusNotImplemented, "Upgrade is not supported")
		return
	}
	h.proxy.ServeHTTP(w, r)
}

func isUpgrade(r *http.Request) bool {
	if r.Header.Get("Upgrade") != "" {
		return true
	}
	for _, value := range r.Header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

func cgiProto(r *http.Request) string {
	if r.TLS != nil || os.Getenv("HTTPS") != "" {
		return "https"
	}
	return "http"
}
