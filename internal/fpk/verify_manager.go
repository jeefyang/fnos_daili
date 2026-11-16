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
	"io"
	"strings"

	"github.com/jeefyang/fnos_daili/internal/spec"
)

// VerifyManager checks the manager fpk: an embedded manager and proxy binary,
// a root privilege file and an iframe entry under the CGI prefix.
func VerifyManager(data []byte) (VerifyResult, error) {
	var zero VerifyResult
	members, err := readPackage(data)
	if err != nil {
		return zero, err
	}
	for name := range outerMembers {
		if _, ok := members[name]; !ok {
			return zero, fmt.Errorf("missing outer member %q", name)
		}
	}
	for name := range members {
		if !outerMembers[name] {
			return zero, fmt.Errorf("unexpected outer member %q", name)
		}
	}
	manifest, err := parseManifest(members["manifest"].data)
	if err != nil {
		return zero, err
	}
	if manifest["appname"] != spec.ManagerAppName {
		return zero, fmt.Errorf("manifest appname = %q", manifest["appname"])
	}
	launch := spec.ManagerAppName + ".main"
	if manifest["desktop_applaunchname"] != launch {
		return zero, fmt.Errorf("manifest desktop_applaunchname = %q", manifest["desktop_applaunchname"])
	}
	if manifest["checkport"] != "false" {
		return zero, fmt.Errorf("manifest checkport = %q", manifest["checkport"])
	}
	if manifest["ctl_stop"] != "true" {
		return zero, fmt.Errorf("manifest ctl_stop = %q, want true", manifest["ctl_stop"])
	}
	for _, forbidden := range []string{"service_port", "install_type", "arch"} {
		if _, ok := manifest[forbidden]; ok {
			return zero, fmt.Errorf("manifest must not contain %q", forbidden)
		}
	}
	sum := md5.Sum(members["app.tgz"].data)
	wantSum := hex.EncodeToString(sum[:])
	if manifest["checksum"] != wantSum {
		return zero, fmt.Errorf("manifest checksum %q does not match app.tgz %q", manifest["checksum"], wantSum)
	}
	if err := checkIconPNG("ICON.PNG", members["ICON.PNG"].data, 64); err != nil {
		return zero, err
	}
	if err := checkIconPNG("ICON_256.PNG", members["ICON_256.PNG"].data, 256); err != nil {
		return zero, err
	}
	if string(members["config/privilege"].data) != managerPrivilegeJSON {
		return zero, fmt.Errorf("manager config/privilege must be run-as root")
	}
	if string(members["config/resource"].data) != "{}" {
		return zero, fmt.Errorf("config/resource must be {}")
	}
	for name, want := range map[string]int64{
		"manifest": 0o644, "app.tgz": 0o644, "cmd/main": 0o755,
		"cmd/install_callback": 0o755,
	} {
		if got := members[name].mode & 0o777; got != want {
			return zero, fmt.Errorf("outer member %q mode %o, want %o", name, got, want)
		}
	}
	if !members["wizard/"].dir {
		return zero, fmt.Errorf("wizard/ must be a directory")
	}
	app, err := readManagerAppTGZ(members["app.tgz"].data)
	if err != nil {
		return zero, err
	}
	if err := checkIconPNG("ui/images/icon_64.png", app["ui/images/icon_64.png"].data, 64); err != nil {
		return zero, err
	}
	if err := checkIconPNG("ui/images/icon_256.png", app["ui/images/icon_256.png"].data, 256); err != nil {
		return zero, err
	}
	platform := manifest["platform"]
	if err := checkELF(app["bin/manager"].data, platform); err != nil {
		return zero, fmt.Errorf("bin/manager: %w", err)
	}
	if err := checkELF(app["bin/proxy"].data, platform); err != nil {
		return zero, fmt.Errorf("bin/proxy: %w", err)
	}
	var ui map[string]any
	if err := json.Unmarshal(app["ui/config"].data, &ui); err != nil {
		return zero, fmt.Errorf("ui/config: %w", err)
	}
	urls, _ := ui[".url"].(map[string]any)
	if len(urls) != 1 {
		return zero, fmt.Errorf("ui/config must contain exactly one entry, got %d", len(urls))
	}
	entryAny, ok := urls[launch]
	if !ok {
		return zero, fmt.Errorf("ui/config key does not match %q", launch)
	}
	entry, _ := entryAny.(map[string]any)
	if s, _ := entry["type"].(string); s != "iframe" {
		return zero, fmt.Errorf("ui/config type = %q, want iframe", s)
	}
	wantURL := "/cgi/ThirdParty/" + spec.ManagerAppName + "/index.cgi/"
	if entry["url"] != wantURL {
		return zero, fmt.Errorf("ui/config url = %v, want %q", entry["url"], wantURL)
	}
	if _, ok := entry["port"]; ok {
		return zero, fmt.Errorf("ui/config must omit port for a CGI entry")
	}
	sum256 := sha256.Sum256(data)
	return VerifyResult{
		AppName:              spec.ManagerAppName,
		Version:              manifest["version"],
		Platform:             platform,
		DesktopAppLaunchName: launch,
		SHA256:               hex.EncodeToString(sum256[:]),
		Size:                 int64(len(data)),
		AppTGZChecksum:       wantSum,
		ProxySize:            int64(len(app["bin/proxy"].data)),
		ManagerSize:          int64(len(app["bin/manager"].data)),
	}, nil
}

func readManagerAppTGZ(data []byte) (map[string]member, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("app.tgz is not gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string]member{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("app.tgz: %w", err)
		}
		if strings.Contains(h.Name, "..") || strings.HasPrefix(h.Name, "/") {
			return nil, fmt.Errorf("unsafe app member %q", h.Name)
		}
		if _, ok := out[h.Name]; ok {
			return nil, fmt.Errorf("duplicate app member %q", h.Name)
		}
		if h.Typeflag == tar.TypeDir {
			out[h.Name] = member{mode: h.Mode, dir: true}
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("app member %q has unsupported type %d", h.Name, h.Typeflag)
		}
		if h.Size > maxMemberBytes {
			return nil, fmt.Errorf("app member %q is too large", h.Name)
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxMemberBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", h.Name, err)
		}
		out[h.Name] = member{data: b, mode: h.Mode}
	}
	for _, name := range []string{"bin/manager", "bin/proxy", "ui/config", "ui/images/icon_64.png", "ui/images/icon_256.png"} {
		if _, ok := out[name]; !ok {
			return nil, fmt.Errorf("missing app member %q", name)
		}
	}
	return out, nil
}
