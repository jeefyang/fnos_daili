package manager

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jeefyang/fnos_daili/internal/cli"
	"github.com/jeefyang/fnos_daili/internal/dnsx"
	"github.com/jeefyang/fnos_daili/internal/favicon"
	"github.com/jeefyang/fnos_daili/internal/guard"
	"github.com/jeefyang/fnos_daili/internal/iconlib"
	"github.com/jeefyang/fnos_daili/internal/spec"
	"github.com/jeefyang/fnos_daili/internal/state"
	"github.com/jeefyang/fnos_daili/internal/target"
)

// EntryPrefix is the manager CGI mount point.
const EntryPrefix = "/cgi/ThirdParty/daili.manager/index.cgi"

// Handler returns the manager's HTTP handler, including the embedded UI.
func (s *Service) Handler(ui http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/targets", s.handleList)
	mux.HandleFunc("POST /api/targets", s.handleCreate)
	mux.HandleFunc("GET /api/targets/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/targets/{id}", s.handleUpdate)
	mux.HandleFunc("GET /api/targets/{id}/icon", s.handleTargetIcon)
	mux.HandleFunc("GET /api/default-icon", s.handleDefaultIcon)
	mux.HandleFunc("POST /api/targets/{id}/apply", s.handleApply)
	mux.HandleFunc("POST /api/targets/{id}/delete", s.handleDelete)
	mux.HandleFunc("POST /api/targets/{id}/rollback", s.handleRollback)
	mux.HandleFunc("GET /api/targets/{id}/package", s.handlePackage)
	mux.HandleFunc("GET /api/targets/{id}/log", s.handleLog)
	mux.HandleFunc("POST /api/targets/{id}/test-target", s.handleTestTarget)
	mux.HandleFunc("POST /api/test-target", s.handleTestTarget)
	mux.HandleFunc("POST /api/import/preview", s.handleImportPreview)
	mux.HandleFunc("POST /api/import", s.handleImport)
	mux.HandleFunc("GET /api/import/{id}", s.handleBatch)
	mux.HandleFunc("GET /api/export", s.handleExport)
	mux.HandleFunc("GET /api/targets/{id}/export", s.handleExportOne)
	mux.HandleFunc("POST /api/favicon", s.handleFavicon)
	mux.HandleFunc("POST /api/icon-library", s.handleIconLibrary)
	mux.HandleFunc("GET /api/appnames/check", s.handleAppNameCheck)
	mux.HandleFunc("GET /api/volumes", s.handleVolumes)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
	mux.HandleFunc("GET /api/network", s.handleNetwork)
	mux.HandleFunc("GET /api/operations/{id}", s.handleOperation)
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /index.html", s.handleIndex)
	mux.Handle("/", ui)
	return s.withCSRF(mux)
}

func (s *Service) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, strings.Replace(UIHTML(), "{{CSRF}}", s.token, 1))
}

func (s *Service) withCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secure := requestIsHTTPS(r)
		if c, err := r.Cookie("daili_csrf"); err != nil || c.Value == "" {
			http.SetCookie(w, &http.Cookie{
				Name:     "daili_csrf",
				Value:    s.token,
				Path:     EntryPrefix + "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
				Secure:   secure,
			})
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.token)) != 1 {
				writeError(w, http.StatusForbidden, "csrf_failed", "CSRF token 不匹配")
				return
			}
			if !sameOrigin(r) {
				// CGI 由 fnOS 的 Web 服务器转发，HTTP_HOST 可能是 127.0.0.1；
				// CSRF token 仍是主校验，这里只对回环上游放宽来源检查。
				if r.Header.Get("X-Forwarded-Host") != "" || !isLoopbackHost(r.Host) {
					s.logger.Printf("origin mismatch host=%q origin=%q referer=%q x-forwarded-host=%q", r.Host, r.Header.Get("Origin"), r.Header.Get("Referer"), r.Header.Get("X-Forwarded-Host"))
					writeError(w, http.StatusForbidden, "origin_mismatch", "请求来源不匹配")
					return
				}
				s.logger.Printf("origin mismatch allowed for cgi upstream host=%q origin=%q", r.Host, r.Header.Get("Origin"))
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if p := r.Header.Get("X-Forwarded-Proto"); strings.EqualFold(p, "https") {
		return true
	}
	return strings.EqualFold(os.Getenv("HTTPS"), "on")
}

func sameOrigin(r *http.Request) bool {
	raw := r.Header.Get("Origin")
	if raw == "" {
		raw = r.Header.Get("Referer")
	}
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	candidates := []string{r.Host}
	for _, h := range r.Header.Values("X-Forwarded-Host") {
		for _, part := range strings.Split(h, ",") {
			candidates = append(candidates, strings.TrimSpace(part))
		}
	}
	for _, h := range candidates {
		if h != "" && equalHost(u.Host, h) {
			return true
		}
	}
	return false
}

// equalHost compares hostnames without ports. Origin omits default ports while
// CGI HTTP_HOST may include them, and the fnOS web server may rewrite Host.
func equalHost(a, b string) bool {
	return strings.EqualFold(hostOnly(a), hostOnly(b))
}

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(h, "[]")
}

func isLoopbackHost(h string) bool {
	host := hostOnly(h)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	ok := false
	if s.cli.Path != "" {
		if _, err := s.cli.List(ctx); err == nil {
			ok = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": s.cfg.Version, "cli_path": s.cli.Path, "cli_ok": ok,
	})
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), cliQueryTimeout)
	defer cancel()
	s.reconcile(ctx, false)
	out := s.List()
	if s.cli.Path == "" {
		out.CLIError = "未找到 appcenter-cli"
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var in SpecInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	view, err := s.Create(in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	v, ok := s.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "目标不存在")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleDefaultIcon serves the built-in icon so the browser can preview it
// without bundling a second copy of the PNG in the JS.
func (s *Service) handleDefaultIcon(w http.ResponseWriter, r *http.Request) {
	data := s.cfg.Icon256
	if r.URL.Query().Get("size") == "64" {
		data = s.cfg.Icon64
	}
	serveIcon(w, data)
}

func (s *Service) handleTargetIcon(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	t := s.findLocked(r.PathValue("id"))
	var data []byte
	if t != nil {
		data = t.CustomIcon256
		if r.URL.Query().Get("size") == "64" {
			data = t.CustomIcon64
		}
	}
	s.mu.Unlock()
	if len(data) == 0 {
		writeError(w, http.StatusNotFound, "no_icon", "该应用使用默认图标")
		return
	}
	serveIcon(w, data)
}

func serveIcon(w http.ResponseWriter, data []byte) {
	if len(data) == 0 {
		writeError(w, http.StatusNotFound, "no_icon", "没有可用图标")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var in SpecInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	v, err := s.Update(r.PathValue("id"), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Service) handleApply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind string `json:"kind"`
	}
	_ = decodeJSON(r, &body)
	op, err := s.Apply(r.PathValue("id"), body.Kind)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"operation_id": op})
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	op, err := s.Delete(r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"operation_id": op})
}

func (s *Service) handleRollback(w http.ResponseWriter, r *http.Request) {
	op, err := s.Rollback(r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"operation_id": op})
}

func (s *Service) handleOperation(w http.ResponseWriter, r *http.Request) {
	op, ok := s.Operation(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "操作不存在")
		return
	}
	writeJSON(w, http.StatusOK, op)
}

// Operation returns the current state of an asynchronous operation.
func (s *Service) Operation(id string) (state.Operation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if op, ok := s.done[id]; ok {
		return op, true
	}
	tid, ok := s.ops[id]
	if !ok {
		for i := range s.st.Targets {
			op := s.st.Targets[i].Runtime.LastOperation
			if op != nil && op.ID == id {
				return *op, true
			}
		}
		return state.Operation{}, false
	}
	t := s.findLocked(tid)
	if t == nil || t.Runtime.LastOperation == nil || t.Runtime.LastOperation.ID != id {
		return state.Operation{}, false
	}
	return *t.Runtime.LastOperation, true
}

func (s *Service) handlePackage(w http.ResponseWriter, r *http.Request) {
	t, ok := s.getTarget(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "目标不存在")
		return
	}
	path := t.Runtime.PackagePath
	if path == "" {
		writeError(w, http.StatusNotFound, "no_package", "还没有生成过安装包")
		return
	}
	root := filepath.Join(s.cfg.DataDir, "packages")
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		writeError(w, http.StatusForbidden, "forbidden", "包路径不在允许范围内")
		return
	}
	if _, err := os.Stat(path); err != nil {
		writeError(w, http.StatusNotFound, "no_package", "安装包已被清理")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(path)))
	http.ServeFile(w, r, path)
}

func (s *Service) handleLog(w http.ResponseWriter, r *http.Request) {
	n := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
			if n > 1000 {
				n = 1000
			}
		}
	}
	lines, err := tailFile(filepath.Join(s.cfg.DataDir, "manager.log"), n)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "log_failed", "读取日志失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

func tailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const maxTail = 512 * 1024
	start := info.Size() - maxTail
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTail))
	if err != nil {
		return nil, err
	}
	raw := strings.Split(strings.TrimRight(string(data), string(rune(10))), string(rune(10)))
	if len(raw) > n {
		raw = raw[len(raw)-n:]
	}
	for i := range raw {
		raw[i] = redactLine(raw[i])
	}
	return raw, nil
}

func redactLine(line string) string {
	lower := strings.ToLower(line)
	for _, marker := range []string{"authorization:", "cookie:", "set-cookie:", "password", "passwd", "secret", "token="} {
		if strings.Contains(lower, marker) {
			return "[redacted]"
		}
	}
	return line
}

// importRequest carries a bulk import either as raw JSON text (what the file
// picker produces) or as already-decoded targets.
type importRequest struct {
	Text      string      `json:"text"`
	Targets   []SpecInput `json:"targets"`
	Install   *bool       `json:"install"`
	SkipProbe bool        `json:"skip_probe"`
}

func (req importRequest) items() ([]SpecInput, error) {
	if strings.TrimSpace(req.Text) != "" {
		return parseImportDocument([]byte(req.Text))
	}
	if req.Targets == nil {
		return nil, errors.New("没有要导入的目标")
	}
	return req.Targets, nil
}

func (s *Service) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	var body importRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	items, err := body.items()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_import", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	plan, _, _ := s.PlanImport(ctx, items, s.Settings().DNSServer)
	writeJSON(w, http.StatusOK, plan)
}

func (s *Service) handleImport(w http.ResponseWriter, r *http.Request) {
	var body importRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	items, err := body.items()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_import", err.Error())
		return
	}
	install := true
	if body.Install != nil {
		install = *body.Install
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	view, err := s.ImportBulk(ctx, items, install, body.SkipProbe)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

func (s *Service) handleBatch(w http.ResponseWriter, r *http.Request) {
	view, ok := s.Batch(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "批量任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Service) handleTestTarget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetURL string `json:"target_url"`
		Mode      string `json:"mode"`
		DNSServer string `json:"dns_server"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	norm, err := target.Normalize(body.TargetURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	u, _ := url.Parse(norm)
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	}
	res := map[string]any{"reachable": false, "status": 0, "elapsed_ms": 0, "error": "", "detail": ""}
	if body.Mode != "direct" {
		g := guard.Guard{}
		dnsServer := strings.TrimSpace(body.DNSServer)
		if dnsServer == "" {
			dnsServer = s.Settings().DNSServer
		}
		if dnsServer != "" {
			if err := dnsx.Validate(dnsServer); err != nil {
				writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
				return
			}
			g.Resolver = dnsx.New(dnsServer)
		}
		if err := g.CheckHost(ctx, u.Hostname(), port); err != nil {
			res["error"], res["detail"] = targetCheckError(err)
			s.logger.Printf("test-target %s: %s (%s)", norm, res["error"], res["detail"])
			res["elapsed_ms"] = time.Since(start).Milliseconds()
			writeJSON(w, http.StatusOK, res)
			return
		}
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), strconv.Itoa(port)))
	if err != nil {
		res["error"] = "connect_failed"
		res["detail"] = shortError(err)
		s.logger.Printf("test-target %s: connect_failed (%s)", norm, res["detail"])
	} else {
		conn.Close()
		res["reachable"] = true
	}
	res["elapsed_ms"] = time.Since(start).Milliseconds()
	writeJSON(w, http.StatusOK, res)
}

// handleFavicon fetches the target's site icon on the operator's explicit
// request. It only runs when the form asks for it and reuses the same address
// guard and pinned DNS server as the proxy runtime.
func (s *Service) handleFavicon(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetURL     string `json:"target_url"`
		DNSServer     string `json:"dns_server"`
		SkipTLSVerify bool   `json:"skip_tls_verify"`
		AllowLoopback bool   `json:"allow_loopback"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	norm, err := target.Normalize(body.TargetURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return
	}
	dnsServer := strings.TrimSpace(body.DNSServer)
	if dnsServer == "" {
		dnsServer = s.Settings().DNSServer
	}
	if dnsServer != "" {
		if err := dnsx.Validate(dnsServer); err != nil {
			writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	g := &guard.Guard{AllowLoopback: body.AllowLoopback}
	if dnsServer != "" {
		g.Resolver = dnsx.New(dnsServer)
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           g.DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: body.SkipTLSVerify}, // #nosec G402 - explicit per-target opt-in.
		},
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
	set, err := favicon.Fetch(ctx, client, norm)
	if err != nil {
		status, code := http.StatusBadGateway, "favicon_failed"
		if errors.Is(err, favicon.ErrNotFound) {
			status, code = http.StatusNotFound, "favicon_not_found"
		}
		msg := shortError(err)
		s.logger.Printf("favicon %s: %s", norm, msg)
		writeError(w, status, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"icon_data": "data:image/png;base64," + base64.StdEncoding.EncodeToString(set.Icon256),
	})
}

// handleIconLibrary looks the target's icon up in a public icon library, the
// fallback watchcow uses when a site only ships a tiny favicon.
func (s *Service) handleIconLibrary(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AppName     string `json:"appname"`
		DisplayName string `json:"display_name"`
		DNSServer   string `json:"dns_server"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	names := s.iconLibraryNames(body.AppName, body.DisplayName)
	if len(names) == 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "appname or display_name is required")
		return
	}
	dnsServer := strings.TrimSpace(body.DNSServer)
	if dnsServer == "" {
		dnsServer = s.Settings().DNSServer
	}
	if dnsServer != "" {
		if err := dnsx.Validate(dnsServer); err != nil {
			writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
			return
		}
	}
	base := strings.TrimSpace(s.cfg.IconLibraryBase)
	if base == "" {
		base = iconlib.DefaultBase
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	// The operator chose this base, so a loopback mirror is intentional; an
	// ordinary public library still goes through the address guard.
	g := &guard.Guard{AllowLoopback: isLoopbackOrigin(base)}
	if dnsServer != "" {
		g.Resolver = dnsx.New(dnsServer)
	}
	set, slug, err := iconlib.Fetch(ctx, outboundClient(g, false), base, names)
	if err != nil {
		status, code := http.StatusBadGateway, "icon_library_failed"
		if errors.Is(err, iconlib.ErrNotFound) {
			status, code = http.StatusNotFound, "icon_library_not_found"
		}
		msg := shortError(err)
		s.logger.Printf("icon library %v: %s", names, msg)
		writeError(w, status, code, msg)
		return
	}
	s.logger.Printf("icon library: %v matched %q", names, slug)
	writeJSON(w, http.StatusOK, map[string]any{
		"icon_data": "data:image/png;base64," + base64.StdEncoding.EncodeToString(set.Icon256),
		"slug":      slug,
	})
}

// iconLibraryNames builds the lookup candidates: display name first, then the
// appname with and without the configured global affixes.
func (s *Service) iconLibraryNames(appname, display string) []string {
	names := make([]string, 0, 4)
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" {
			names = append(names, v)
		}
	}
	add(display)
	add(appname)
	settings := s.Settings()
	trimmed := strings.TrimPrefix(strings.TrimSuffix(appname, settings.AppNameSuffix), settings.AppNamePrefix)
	if trimmed != appname {
		add(trimmed)
		add(strings.NewReplacer(".", " ", "_", "-").Replace(trimmed))
	}
	return names
}

// isLoopbackOrigin reports whether rawURL points at the local machine.
func isLoopbackOrigin(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// outboundClient builds the HTTP client used for operator-triggered outbound
// fetches: it dials through the guard, honours the pinned DNS server, and
// never follows more than five redirects.
func outboundClient(g *guard.Guard, skipTLS bool) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           g.DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: skipTLS}, // #nosec G402 - explicit opt-in.
		},
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

// targetCheckError turns a guard failure into a stable category plus a short,
// secret-free hint for the operator. DNS errors are split into timeout and
// generic failure so a silent NAS resolver can be told apart from a bad host.
func targetCheckError(err error) (string, string) {
	var ge *guard.Error
	if errors.As(err, &ge) {
		return ge.Reason, ""
	}
	var re *guard.ResolveError
	if errors.As(err, &re) {
		if isTimeout(re.Err) {
			return "dns_timeout", shortError(re.Err)
		}
		return "dns_failure", shortError(re.Err)
	}
	return "blocked", shortError(err)
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// shortError collapses an error to one line of at most 160 bytes.
func shortError(err error) string {
	if err == nil {
		return ""
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func (s *Service) handleAppNameCheck(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("appname")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ok, reason := s.AppNameCheck(ctx, name)
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "reason": reason})
}

func (s *Service) handleVolumes(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	def, list := s.VolumeInfo(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"default": def, "volumes": list})
}

func (s *Service) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Settings())
}

func (s *Service) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var in state.Settings
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	out, err := s.SaveSettings(in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleNetwork reports what the server can tell about the browser's network.
// The manager is reached through the fnOS CGI proxy, which stamps
// X-Daili-Client-IP from the TCP peer. A public client IP means the proxy's
// independent TCP port is normally not reachable from that browser.
func (s *Service) handleNetwork(w http.ResponseWriter, r *http.Request) {
	ip := clientIPFromRequest(r)
	var lan any
	if ip != nil {
		lan = ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
	}
	client := ""
	if ip != nil {
		client = ip.String()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"client_ip": client,
		"lan":       lan,
		"known":     ip != nil,
	})
}

func clientIPFromRequest(r *http.Request) net.IP {
	candidates := []string{
		r.Header.Get("X-Daili-Client-IP"),
		r.Header.Get("X-Forwarded-For"),
		r.Header.Get("X-Real-IP"),
		r.RemoteAddr,
	}
	for _, candidate := range candidates {
		if ip := parseClientIP(candidate); ip != nil {
			return ip
		}
	}
	return nil
}

func parseClientIP(raw string) net.IP {
	raw = strings.TrimSpace(strings.Split(raw, ",")[0])
	if raw == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.Trim(raw, "[]")
	if i := strings.IndexByte(raw, '%'); i >= 0 {
		raw = raw[:i]
	}
	return net.ParseIP(raw)
}

func decodeJSON(r *http.Request, out any) error {
	defer io.Copy(io.Discard, r.Body)
	// Icon uploads arrive as a base64 data URL; 8 MiB covers a 2 MiB image
	// plus JSON overhead while still bounding the request body.
	dec := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeDownload sends a JSON document as a file attachment.
func writeDownload(w http.ResponseWriter, name, contentType string, v any) {
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotFound):
		writeError(w, http.StatusNotFound, "not_found", "目标不存在")
	case errors.Is(err, errConflict):
		writeError(w, http.StatusConflict, "conflict", "该目标已有操作在进行")
	default:
		var se *spec.Error
		if errors.As(err, &se) {
			writeError(w, http.StatusBadRequest, "validation_failed", se.Error())
			return
		}
		var ce *cli.Error
		if errors.As(err, &ce) {
			writeError(w, http.StatusBadGateway, ce.Code, ce.Message)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}

// reconcile refreshes runtime states from appcenter-cli list. It never mutates
// installed apps, only the state file.
func (s *Service) reconcile(ctx context.Context, startup bool) {
	if s.cli.Path == "" {
		return
	}
	apps, err := s.cli.List(ctx)
	if err != nil {
		s.logger.Printf("reconcile: list failed: %v", err)
		return
	}
	byName := map[string]cli.App{}
	for _, a := range apps {
		byName[a.AppName] = a
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	managed := map[string]bool{}
	for i := range s.st.Targets {
		t := &s.st.Targets[i]
		managed[t.Spec.AppName] = true
		if s.running[t.Spec.ID] {
			// A live operation owns this target's runtime state; listing must not
			// mistake the brief gap during reinstall for a missing app or mark
			// the operation interrupted.
			continue
		}
		app, ok := byName[t.Spec.AppName]
		if !ok {
			if t.Runtime.State == state.StateRunning || t.Runtime.State == state.StateInstalledNotRunning || t.Runtime.State == state.StateStarting {
				t.Runtime.State = state.StateMissing
				t.Runtime.UpdatedAt = s.now()
				changed = true
			}
			if t.Runtime.LastOperation != nil && t.Runtime.LastOperation.Status == state.OpRunning {
				t.Runtime.LastOperation.Status = state.OpFailed
				t.Runtime.LastOperation.Step = "interrupted"
				t.Runtime.LastOperation.Error = "托管台进程重启，操作中断"
				changed = true
			}
			continue
		}
		st := normalizeAppStatus(app.Status)
		if t.Runtime.LastOperation != nil && t.Runtime.LastOperation.Status == state.OpRunning {
			t.Runtime.LastOperation.Status = state.OpFailed
			t.Runtime.LastOperation.Step = "interrupted"
			t.Runtime.LastOperation.Error = "托管台进程重启，操作中断"
			changed = true
		}
		if t.Runtime.State != st {
			t.Runtime.State = st
			t.Runtime.UpdatedAt = s.now()
			changed = true
		}
		if app.Version != "" && app.Version != t.Runtime.InstalledVersion && st == state.StateRunning {
			t.Runtime.InstalledVersion = app.Version
			changed = true
		}
	}
	unmanaged := make([]UnmanagedApp, 0)
	for _, a := range apps {
		if managed[a.AppName] {
			continue
		}
		unmanaged = append(unmanaged, UnmanagedApp{AppName: a.AppName, DisplayName: a.DisplayName, Version: a.Version, Status: a.Status})
	}
	s.unmanaged = unmanaged
	if changed {
		if err := s.saveLocked(); err != nil {
			s.logger.Printf("reconcile: save: %v", err)
		}
	}
	_ = startup
}

func normalizeAppStatus(st string) string {
	switch strings.ToLower(strings.TrimSpace(st)) {
	case "running":
		return state.StateRunning
	case "starting":
		return state.StateStarting
	case "stopping":
		return state.StateUninstalling
	case "stopped":
		return state.StateInstalledNotRunning
	case "", "noinstall":
		return state.StateMissing
	default:
		return state.StateInstalledNotRunning
	}
}

var _ = fmt.Sprintf
