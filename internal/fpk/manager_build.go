package fpk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jeefyang/fnos_daili/internal/spec"
)

func buildManagerAppTGZ(opts ManagerOptions) ([]byte, error) {
	uiBytes, err := buildManagerUIConfig()
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
	if err := writeFile(tw, "bin/manager", 0o755, opts.ManagerBinary); err != nil {
		return nil, err
	}
	if err := writeFile(tw, "bin/proxy", 0o755, opts.ProxyBinary); err != nil {
		return nil, err
	}
	if err := writeFile(tw, "ui/config", 0o644, uiBytes); err != nil {
		return nil, err
	}
	if err := writeFile(tw, "ui/images/icon_64.png", 0o644, opts.Icon64); err != nil {
		return nil, err
	}
	if err := writeFile(tw, "ui/images/icon_256.png", 0o644, opts.Icon256); err != nil {
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

func buildManagerUIConfig() ([]byte, error) {
	entryName := spec.ManagerAppName + ".main"
	cfg := uiConfig{URL: map[string]uiEntry{
		entryName: {
			Title:     "代理打包器",
			Icon:      "images/icon_{0}.png",
			Type:      "iframe",
			Protocol:  "http",
			Port:      "",
			URL:       "/cgi/ThirdParty/" + spec.ManagerAppName + "/index.cgi/",
			AllUsers:  false,
			NoDisplay: false,
		},
	}}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func buildManagerManifest(platform, version, checksum string) string {
	lines := []string{
		"appname=" + spec.ManagerAppName,
		"version=" + version,
		"display_name=代理打包器",
		"desc=生成并安装任意 ip:port 的飞牛代理应用",
		"platform=" + platform,
		"source=thirdparty",
		"maintainer=fnos_daili",
		"distributor=fnos_daili",
		"os_min_version=0.9.0",
		"desktop_uidir=ui",
		"desktop_applaunchname=" + spec.ManagerAppName + ".main",
		"ctl_stop=true",
		"checkport=false",
		"checksum=" + checksum,
	}
	return strings.Join(lines, "\n") + "\n"
}

func managerMainScript() string {
	return fmt.Sprintf(`#!/bin/bash
set -u
APP="%s"
DESK="${TRIM_APPDEST:-/tmp/$APP}"
VAR="${TRIM_PKGVAR:-/tmp/$APP-var}"
PID="$VAR/$APP.pid"
SOCK="$VAR/manager.sock"
LOG="$VAR/manager.log"

start() {
  mkdir -p "$VAR" || exit 1
  chmod 700 "$VAR"
  if [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null; then
    [ -S "$SOCK" ] && exit 0
  fi
  rm -f "$SOCK" "$PID"
  nohup "$DESK/bin/manager" --mode serve --socket "$SOCK" >>"$LOG" 2>&1 &
  echo $! >"$PID"
  chmod 600 "$PID"
  for _ in $(seq 1 50); do
    [ -S "$SOCK" ] && exit 0
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
  rm -f "$SOCK"
  exit 0
}

status() {
  if [ -f "$PID" ] && kill -0 "$(cat "$PID")" 2>/dev/null && [ -S "$SOCK" ]; then
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
`, spec.ManagerAppName)
}

func managerInstallCallbackScript() string {
	return `#!/bin/bash
set -u
DESK="${TRIM_APPDEST:?}"
VAR="${TRIM_PKGVAR:?}"
mkdir -p "$DESK/ui" "$VAR"
cat >"$DESK/ui/index.cgi" <<'CGI'
#!/bin/bash
exec "${TRIM_APPDEST}/bin/manager" --mode cgi --socket "${TRIM_PKGVAR}/manager.sock"
CGI
chmod 755 "$DESK/ui/index.cgi"
# The manager has no service_port, so appcenter may not start it automatically.
# Start it here as well; cmd/main is idempotent (pid + socket check).
if [ -x "$DESK/cmd/main" ]; then
  "$DESK/cmd/main" start >/dev/null 2>&1 || true
fi
exit 0
`
}
