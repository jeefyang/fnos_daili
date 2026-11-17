package fpk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"strings"
	"time"

	"github.com/jeefyang/fnos_daili/internal/spec"
)

// fnOS validates config/privilege against its template shape, including the
// four-space indentation; a minified object can be rejected at install time.
const privilegeJSON = "{\n    \"defaults\":\n    {\n        \"run-as\": \"package\"\n    }\n}"

// managerPrivilegeJSON runs the manager as root: it calls appcenter-cli, which
// needs root to reach the appcenter rpcbroker socket (V1 #10).
const managerPrivilegeJSON = "{\n    \"defaults\":\n    {\n        \"run-as\": \"root\"\n    }\n}"

type Assets struct {
	ProxyBinary []byte
	Icon64      []byte
	Icon256     []byte
}

type BuildOptions struct {
	Spec     spec.ProxySpec
	Assets   Assets
	Platform string // x86 or arm; empty means x86
}

type Result struct {
	SHA256   string
	Size     int64
	Manifest string
}

// ManagerOptions builds the daili.manager fpk. It embeds both binaries so the
// manager can generate proxy packages without relying on external files.
type ManagerOptions struct {
	ManagerBinary []byte
	ProxyBinary   []byte
	Icon64        []byte
	Icon256       []byte
	Version       string
	Platform      string // x86 or arm; empty means x86
}

// BuildManager writes the manager fpk.
func BuildManager(w io.Writer, opts ManagerOptions) (Result, error) {
	var zero Result
	if len(opts.ManagerBinary) == 0 {
		return zero, fmt.Errorf("manager binary is empty")
	}
	if len(opts.ProxyBinary) == 0 {
		return zero, fmt.Errorf("proxy binary is empty")
	}
	if err := checkIcon("ICON.PNG", opts.Icon64, 64); err != nil {
		return zero, err
	}
	if err := checkIcon("ICON_256.PNG", opts.Icon256, 256); err != nil {
		return zero, err
	}
	platform := opts.Platform
	if platform == "" {
		platform = "x86"
	}
	if platform != "x86" && platform != "arm" {
		return zero, fmt.Errorf("platform must be x86 or arm")
	}
	version := opts.Version
	if version == "" {
		version = "1.0.0"
	}
	appTGZ, err := buildManagerAppTGZ(opts)
	if err != nil {
		return zero, err
	}
	checksum := md5.Sum(appTGZ)
	manifest := buildManagerManifest(platform, version, hex.EncodeToString(checksum[:]))
	var pkg bytes.Buffer
	if err := writeOuter(&pkg, opts.Icon64, opts.Icon256, managerPrivilegeJSON, appTGZ, manifest, managerMainScript(), managerInstallCallbackScript()); err != nil {
		return zero, err
	}
	if _, err := w.Write(pkg.Bytes()); err != nil {
		return zero, err
	}
	sum := sha256.Sum256(pkg.Bytes())
	return Result{SHA256: hex.EncodeToString(sum[:]), Size: int64(pkg.Len()), Manifest: manifest}, nil
}

// BuildProxy writes one proxy fpk. The package is deterministic for the same
// inputs and uses only fixed timestamps.
func BuildProxy(w io.Writer, opts BuildOptions) (Result, error) {
	var zero Result

	s, err := spec.NormalizeProxySpec(opts.Spec)
	if err != nil {
		return zero, err
	}
	if !spec.ValidPort(s.Port) {
		return zero, fmt.Errorf("proxy port %d is outside %d-%d", s.Port, spec.PortMin, spec.PortMax)
	}
	if len(opts.Assets.ProxyBinary) == 0 {
		return zero, fmt.Errorf("proxy binary is empty")
	}
	if err := checkIcon("ICON.PNG", opts.Assets.Icon64, 64); err != nil {
		return zero, err
	}
	if err := checkIcon("ICON_256.PNG", opts.Assets.Icon256, 256); err != nil {
		return zero, err
	}
	platform := opts.Platform
	if platform == "" {
		platform = "x86"
	}
	if platform != "x86" && platform != "arm" {
		return zero, fmt.Errorf("platform must be x86 or arm")
	}

	appTGZ, err := buildAppTGZ(s, opts.Assets)
	if err != nil {
		return zero, err
	}
	checksum := md5.Sum(appTGZ)
	manifest := buildManifest(s, platform, hex.EncodeToString(checksum[:]))

	var pkg bytes.Buffer
	if err := writeOuter(&pkg, opts.Assets.Icon64, opts.Assets.Icon256, privilegeJSON, appTGZ, manifest, mainScript(s.AppName, s.Port), noopScript()); err != nil {
		return zero, err
	}
	if _, err := w.Write(pkg.Bytes()); err != nil {
		return zero, err
	}
	sum := sha256.Sum256(pkg.Bytes())
	return Result{SHA256: hex.EncodeToString(sum[:]), Size: int64(pkg.Len()), Manifest: manifest}, nil
}

func checkIcon(name string, data []byte, want int) error {
	if len(data) == 0 {
		return fmt.Errorf("%s is empty", name)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%s is not a PNG: %w", name, err)
	}
	if cfg.Width != want || cfg.Height != want {
		return fmt.Errorf("%s must be %dx%d, got %dx%d", name, want, want, cfg.Width, cfg.Height)
	}
	return nil
}

func writeOuter(w io.Writer, icon64, icon256 []byte, privilege string, appTGZ []byte, manifest, mainBody, callbackBody string) error {
	gw := gzip.NewWriter(w)
	tw := tar.NewWriter(gw)

	write := func(name string, mode int64, data []byte) error {
		return writeFile(tw, name, mode, data)
	}
	if err := write("manifest", 0o644, []byte(manifest)); err != nil {
		return err
	}
	if err := write("ICON.PNG", 0o644, icon64); err != nil {
		return err
	}
	if err := write("ICON_256.PNG", 0o644, icon256); err != nil {
		return err
	}
	if err := write("config/privilege", 0o644, []byte(privilege)); err != nil {
		return err
	}
	if err := write("config/resource", 0o644, []byte("{}")); err != nil {
		return err
	}
	if err := write("app.tgz", 0o644, appTGZ); err != nil {
		return err
	}
	if err := write("cmd/main", 0o755, []byte(mainBody)); err != nil {
		return err
	}
	for _, name := range []string{
		"cmd/install_init", "cmd/install_callback", "cmd/uninstall_init",
		"cmd/uninstall_callback", "cmd/upgrade_init", "cmd/upgrade_callback",
		"cmd/config_init", "cmd/config_callback",
	} {
		body := noopScript()
		if name == "cmd/install_callback" {
			body = callbackBody
		}
		if err := write(name, 0o755, []byte(body)); err != nil {
			return err
		}
	}
	if err := writeDir(tw, "wizard/"); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}
	return nil
}

func buildAppTGZ(s spec.ProxySpec, assets Assets) ([]byte, error) {
	uiBytes, err := buildUIConfig(s)
	if err != nil {
		return nil, err
	}
	cfgBytes, err := buildProxyConfig(s)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, dir := range []string{"ui/", "ui/images/", "bin/"} {
		if err := writeDir(tw, dir); err != nil {
			return nil, err
		}
	}
	if err := writeFile(tw, "config.json", 0o600, cfgBytes); err != nil {
		return nil, err
	}
	if err := writeFile(tw, "bin/proxy", 0o755, assets.ProxyBinary); err != nil {
		return nil, err
	}
	if err := writeFile(tw, "ui/config", 0o644, uiBytes); err != nil {
		return nil, err
	}
	if err := writeFile(tw, "ui/images/icon_64.png", 0o644, assets.Icon64); err != nil {
		return nil, err
	}
	if err := writeFile(tw, "ui/images/icon_256.png", 0o644, assets.Icon256); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type uiEntry struct {
	Title     string   `json:"title"`
	Icon      string   `json:"icon"`
	Type      string   `json:"type"`
	Protocol  string   `json:"protocol"`
	Port      string   `json:"port,omitempty"`
	URL       string   `json:"url"`
	AllUsers  bool     `json:"allUsers"`
	NoDisplay bool     `json:"noDisplay"`
	FileTypes []string `json:"fileTypes,omitempty"`
}

type uiConfig struct {
	URL map[string]uiEntry `json:".url"`
}

func buildUIConfig(s spec.ProxySpec) ([]byte, error) {
	entryName := s.AppName + ".main"
	cfg := uiConfig{URL: map[string]uiEntry{
		entryName: {
			Title:     s.EntryTitle,
			Icon:      "images/icon_{0}.png",
			Type:      s.UIType,
			Protocol:  "http",
			Port:      fmt.Sprint(s.Port),
			URL:       "/",
			AllUsers:  s.AllUsers,
			NoDisplay: s.NoDisplay,
			FileTypes: s.FileTypes,
		},
	}}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

type tlsConfig struct {
	SkipVerify bool `json:"skip_verify"`
}

type guardConfig struct {
	AllowLoopback bool `json:"allow_loopback"`
}

type timeoutsConfig struct {
	Dial           string `json:"dial"`
	TLSHandshake   string `json:"tls_handshake"`
	ResponseHeader string `json:"response_header"`
	Idle           string `json:"idle"`
}

type proxyConfig struct {
	Schema    int             `json:"schema"`
	AppName   string          `json:"appname"`
	Mode      string          `json:"mode"`
	Target    string          `json:"target"`
	DNS       string          `json:"dns,omitempty"`
	TLS       *tlsConfig      `json:"tls,omitempty"`
	Guard     *guardConfig    `json:"guard,omitempty"`
	BasicAuth *spec.BasicAuth `json:"basic_auth,omitempty"`
	Timeouts  timeoutsConfig  `json:"timeouts"`
}

func buildProxyConfig(s spec.ProxySpec) ([]byte, error) {
	cfg := proxyConfig{
		Schema:  1,
		AppName: s.AppName,
		Mode:    s.Mode,
		Target:  s.TargetURL,
		DNS:     s.DNSServer,
		Timeouts: timeoutsConfig{
			Dial:           "10s",
			TLSHandshake:   "10s",
			ResponseHeader: "30s",
			Idle:           "90s",
		},
	}
	if s.Mode == string(spec.ModeReverse) {
		cfg.TLS = &tlsConfig{SkipVerify: s.SkipTLSVerify}
		cfg.Guard = &guardConfig{AllowLoopback: s.AllowLoopback}
		cfg.BasicAuth = s.BasicAuth
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func buildManifest(s spec.ProxySpec, platform, checksum string) string {
	desc := s.Description
	if desc == "" {
		desc = "通过代理访问 " + s.TargetURL
	}
	desc = strings.ReplaceAll(desc, "\r", " ")
	desc = strings.ReplaceAll(desc, "\n", " ")
	lines := []string{
		"appname=" + s.AppName,
		"version=" + s.Version,
		"display_name=" + s.DisplayName,
		"desc=" + desc,
		"platform=" + platform,
		"source=thirdparty",
		"maintainer=fnos_daili",
		"distributor=fnos_daili",
		"os_min_version=0.9.0",
		"desktop_uidir=ui",
		"desktop_applaunchname=" + s.AppName + ".main",
		"service_port=" + fmt.Sprint(s.Port),
		"checkport=false",
		"checksum=" + checksum,
	}
	return strings.Join(lines, "\n") + "\n"
}

func mainScript(appName string, port int) string {
	return fmt.Sprintf(`#!/bin/bash
set -u
APP="%s"
PORT="%d"
DESK="${TRIM_APPDEST:-/tmp/$APP}"
VAR="${TRIM_PKGVAR:-/tmp/$APP-var}"
PID="$VAR/$APP.pid"
LOG="$VAR/$APP.log"

start() {
  mkdir -p "$VAR" || exit 1
  chmod 700 "$VAR"
  if [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null; then
    exit 0
  fi
  rm -f "$PID"
  if [ -f "$DESK/config.json" ]; then
    cp -f "$DESK/config.json" "$VAR/config.json"
    chmod 600 "$VAR/config.json"
  fi
  nohup "$DESK/bin/proxy" --mode serve --listen "0.0.0.0:$PORT" --config "$VAR/config.json" \
    >>"$LOG" 2>&1 &
  echo $! >"$PID"
  chmod 600 "$PID"
  for _ in $(seq 1 50); do
    if ! kill -0 "$(cat "$PID")" 2>/dev/null; then
      break
    fi
    if (echo >"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then
      exit 0
    fi
    sleep 0.1
  done
  exit 1
}

stop() {
  if [ -f "$PID" ]; then
    kill "$(cat "$PID")" 2>/dev/null || true
    for _ in $(seq 1 50); do
      kill -0 "$(cat "$PID")" 2>/dev/null || break
      sleep 0.1
    done
    kill -9 "$(cat "$PID")" 2>/dev/null || true
    rm -f "$PID"
  fi
  exit 0
}

status() {
  if [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null; then
    exit 0
  fi
  exit 3
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  status) status ;;
  *) exit 1 ;;
esac
`, appName, port)
}

func noopScript() string {
	return "#!/bin/bash\nset -u\nexit 0\n"
}

func writeFile(tw *tar.Writer, name string, mode int64, data []byte) error {
	h := &tar.Header{
		Name:     name,
		Mode:     mode,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
		ModTime:  time.Unix(0, 0),
		Format:   tar.FormatUSTAR,
	}
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func writeDir(tw *tar.Writer, name string) error {
	return tw.WriteHeader(&tar.Header{
		Name:     name,
		Mode:     0o755,
		Typeflag: tar.TypeDir,
		ModTime:  time.Unix(0, 0),
		Format:   tar.FormatUSTAR,
	})
}
