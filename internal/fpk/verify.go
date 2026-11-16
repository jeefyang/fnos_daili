package fpk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"os"
	"strings"

	"github.com/jeefyang/fnos_daili/internal/spec"
	"github.com/jeefyang/fnos_daili/internal/target"
)

const (
	maxPackageBytes = 128 << 20
	maxMemberBytes  = 64 << 20
)

// VerifyResult describes the package that Verify accepted.
type VerifyResult struct {
	AppName              string
	Version              string
	Platform             string
	DesktopAppLaunchName string
	SHA256               string
	Size                 int64
	AppTGZChecksum       string
	ProxySize            int64
}

var outerMembers = map[string]bool{
	"manifest":               true,
	"ICON.PNG":               true,
	"ICON_256.PNG":           true,
	"config/privilege":       true,
	"config/resource":        true,
	"app.tgz":                true,
	"cmd/main":               true,
	"cmd/install_init":       true,
	"cmd/install_callback":   true,
	"cmd/uninstall_init":     true,
	"cmd/uninstall_callback": true,
	"cmd/upgrade_init":       true,
	"cmd/upgrade_callback":   true,
	"cmd/config_init":        true,
	"cmd/config_callback":    true,
	"wizard/":                true,
}

// VerifyFile reads and verifies one generated proxy fpk.
func VerifyFile(path string) (VerifyResult, error) {
	var zero VerifyResult
	st, err := os.Stat(path)
	if err != nil {
		return zero, err
	}
	if st.Size() > maxPackageBytes {
		return zero, fmt.Errorf("package is too large: %d bytes", st.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return zero, err
	}
	return Verify(data)
}

// Verify checks the structure and cross-file invariants of a generated proxy
// package. It is intentionally stricter than fnOS install-fpk: V1 showed that
// install-fpk accepts a wrong checksum, a missing config/resource or a
// desktop_applaunchname that does not match ui/config.
func Verify(data []byte) (VerifyResult, error) {
	var zero VerifyResult
	if len(data) > maxPackageBytes {
		return zero, fmt.Errorf("package is too large: %d bytes", len(data))
	}
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
	appName := manifest["appname"]
	if err := spec.ValidateAppName(appName); err != nil {
		return zero, fmt.Errorf("manifest appname: %w", err)
	}
	platform := manifest["platform"]
	if platform != "x86" && platform != "arm" {
		return zero, fmt.Errorf("manifest platform must be x86 or arm, got %q", platform)
	}
	wantLaunch := appName + ".main"
	if manifest["desktop_applaunchname"] != wantLaunch {
		return zero, fmt.Errorf("manifest desktop_applaunchname %q does not match %q", manifest["desktop_applaunchname"], wantLaunch)
	}
	if manifest["checkport"] != "false" {
		return zero, fmt.Errorf("manifest checkport must be false, got %q", manifest["checkport"])
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
	if string(members["config/privilege"].data) != privilegeJSON {
		return zero, fmt.Errorf("config/privilege must match the fnOS template exactly")
	}
	if string(members["config/resource"].data) != "{}" {
		return zero, fmt.Errorf("config/resource must be {}")
	}
	for name, want := range map[string]int64{
		"manifest": 0o644, "ICON.PNG": 0o644, "ICON_256.PNG": 0o644,
		"config/privilege": 0o644, "config/resource": 0o644,
		"app.tgz": 0o644, "cmd/main": 0o755,
		"cmd/install_init": 0o755, "cmd/install_callback": 0o755,
		"cmd/uninstall_init": 0o755, "cmd/uninstall_callback": 0o755,
		"cmd/upgrade_init": 0o755, "cmd/upgrade_callback": 0o755,
		"cmd/config_init": 0o755, "cmd/config_callback": 0o755,
	} {
		if got := members[name].mode & 0o777; got != want {
			return zero, fmt.Errorf("outer member %q mode %o, want %o", name, got, want)
		}
	}
	if !members["wizard/"].dir {
		return zero, fmt.Errorf("wizard/ must be a directory")
	}

	app, err := readAppTGZ(members["app.tgz"].data)
	if err != nil {
		return zero, err
	}
	for name, want := range map[string]int64{
		"config.json": 0o600, "bin/proxy": 0o755,
		"ui/config": 0o644, "ui/images/icon_64.png": 0o644,
		"ui/images/icon_256.png": 0o644,
	} {
		if got := app[name].mode & 0o777; got != want {
			return zero, fmt.Errorf("app member %q mode %o, want %o", name, got, want)
		}
	}
	if err := checkIconPNG("ui/images/icon_64.png", app["ui/images/icon_64.png"].data, 64); err != nil {
		return zero, err
	}
	if err := checkIconPNG("ui/images/icon_256.png", app["ui/images/icon_256.png"].data, 256); err != nil {
		return zero, err
	}
	proxy := app["bin/proxy"].data
	if err := checkELF(proxy, platform); err != nil {
		return zero, err
	}

	var cfg map[string]any
	if err := json.Unmarshal(app["config.json"].data, &cfg); err != nil {
		return zero, fmt.Errorf("app config.json: %w", err)
	}
	if cfg["schema"] != float64(1) || cfg["appname"] != appName {
		return zero, fmt.Errorf("app config.json identity = %v", cfg)
	}
	mode, _ := cfg["mode"].(string)
	if mode != string(spec.ModeReverse) && mode != string(spec.ModeDirect) {
		return zero, fmt.Errorf("app config.json mode = %q", mode)
	}
	targetURL, _ := cfg["target"].(string)
	if _, err := target.Normalize(targetURL); err != nil {
		return zero, fmt.Errorf("app config.json target: %w", err)
	}

	var ui map[string]any
	if err := json.Unmarshal(app["ui/config"].data, &ui); err != nil {
		return zero, fmt.Errorf("app ui/config: %w", err)
	}
	urls, _ := ui[".url"].(map[string]any)
	if len(urls) != 1 {
		return zero, fmt.Errorf("app ui/config must contain exactly one entry, got %d", len(urls))
	}
	entryAny, ok := urls[wantLaunch]
	if !ok {
		return zero, fmt.Errorf("app ui/config key does not match desktop_applaunchname %q", wantLaunch)
	}
	entry, _ := entryAny.(map[string]any)
	if s, _ := entry["type"].(string); s != "url" && s != "iframe" {
		return zero, fmt.Errorf("app ui/config type = %q", s)
	}
	if s, _ := entry["protocol"].(string); s != "http" {
		return zero, fmt.Errorf("app ui/config protocol = %q", s)
	}
	if s, _ := entry["port"].(string); s != "" {
		return zero, fmt.Errorf("app ui/config port must be empty, got %q", s)
	}
	if wantURL := "/cgi/ThirdParty/" + appName + "/index.cgi/"; entry["url"] != wantURL {
		return zero, fmt.Errorf("app ui/config url = %v, want %q", entry["url"], wantURL)
	}
	if s, _ := entry["icon"].(string); s != "images/icon_{0}.png" {
		return zero, fmt.Errorf("app ui/config icon = %q", s)
	}

	sum256 := sha256.Sum256(data)
	return VerifyResult{
		AppName:              appName,
		Version:              manifest["version"],
		Platform:             platform,
		DesktopAppLaunchName: wantLaunch,
		SHA256:               hex.EncodeToString(sum256[:]),
		Size:                 int64(len(data)),
		AppTGZChecksum:       wantSum,
		ProxySize:            int64(len(proxy)),
	}, nil
}

type member struct {
	data []byte
	mode int64
	dir  bool
}

func readPackage(data []byte) (map[string]member, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("package is not gzip: %w", err)
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
			return nil, fmt.Errorf("package tar: %w", err)
		}
		if _, ok := out[h.Name]; ok {
			return nil, fmt.Errorf("duplicate outer member %q", h.Name)
		}
		if h.Size > maxMemberBytes {
			return nil, fmt.Errorf("outer member %q is too large", h.Name)
		}
		if h.Typeflag == tar.TypeDir {
			out[h.Name] = member{mode: h.Mode, dir: true}
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("outer member %q has unsupported type %d", h.Name, h.Typeflag)
		}
		b, err := io.ReadAll(io.LimitReader(tr, maxMemberBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", h.Name, err)
		}
		if len(b) > maxMemberBytes {
			return nil, fmt.Errorf("outer member %q is too large", h.Name)
		}
		out[h.Name] = member{data: b, mode: h.Mode}
	}
	return out, nil
}

func readAppTGZ(data []byte) (map[string]member, error) {
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
		if len(b) > maxMemberBytes {
			return nil, fmt.Errorf("app member %q is too large", h.Name)
		}
		out[h.Name] = member{data: b, mode: h.Mode}
	}
	for _, name := range []string{
		"config.json", "bin/proxy", "ui/config",
		"ui/images/icon_64.png", "ui/images/icon_256.png",
	} {
		if _, ok := out[name]; !ok {
			return nil, fmt.Errorf("missing app member %q", name)
		}
	}
	return out, nil
}

func parseManifest(data []byte) (map[string]string, error) {
	s := string(data)
	if !strings.HasSuffix(s, "\n") {
		return nil, fmt.Errorf("manifest must end with a newline")
	}
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			return nil, fmt.Errorf("manifest contains an empty or comment line")
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || k == "" || strings.TrimSpace(k) != k {
			return nil, fmt.Errorf("manifest line is not key=value: %q", line)
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("manifest key %q is duplicated", k)
		}
		out[k] = v
	}
	for _, k := range []string{
		"appname", "version", "display_name", "desc", "platform", "source",
		"maintainer", "distributor", "os_min_version", "desktop_uidir",
		"desktop_applaunchname", "checkport", "checksum",
	} {
		if _, ok := out[k]; !ok {
			return nil, fmt.Errorf("manifest missing %q", k)
		}
	}
	return out, nil
}

func checkIconPNG(name string, data []byte, want int) error {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%s is not a PNG: %w", name, err)
	}
	if cfg.Width != want || cfg.Height != want {
		return fmt.Errorf("%s must be %dx%d, got %dx%d", name, want, want, cfg.Width, cfg.Height)
	}
	return nil
}

func checkELF(b []byte, platform string) error {
	if len(b) < 64 {
		return fmt.Errorf("bin/proxy is too short to be an ELF")
	}
	if !bytes.Equal(b[:4], []byte{0x7f, 'E', 'L', 'F'}) {
		return fmt.Errorf("bin/proxy is not an ELF")
	}
	if b[4] != 2 || b[5] != 1 {
		return fmt.Errorf("bin/proxy must be a little-endian ELF64")
	}
	typ := binary.LittleEndian.Uint16(b[16:18])
	if typ != 2 && typ != 3 {
		return fmt.Errorf("bin/proxy has unexpected ELF type %d", typ)
	}
	machine := binary.LittleEndian.Uint16(b[18:20])
	wantMachine := uint16(62)
	if platform == "arm" {
		wantMachine = 183
	}
	if machine != wantMachine {
		return fmt.Errorf("bin/proxy machine %d does not match platform %q", machine, platform)
	}
	phoff := binary.LittleEndian.Uint64(b[32:40])
	phentsize := binary.LittleEndian.Uint16(b[54:56])
	phnum := binary.LittleEndian.Uint16(b[56:58])
	if phnum == 0 {
		return nil
	}
	if phentsize < 56 || phoff > uint64(len(b)) || uint64(phentsize)*uint64(phnum) > uint64(len(b))-phoff {
		return fmt.Errorf("bin/proxy has invalid program headers")
	}
	for i := uint16(0); i < phnum; i++ {
		p := b[phoff+uint64(i)*uint64(phentsize):]
		ptype := binary.LittleEndian.Uint32(p[0:4])
		if ptype == 3 { // PT_INTERP
			return fmt.Errorf("bin/proxy is dynamically linked (PT_INTERP)")
		}
	}
	return nil
}
