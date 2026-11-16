package fpk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/jeefyang/fnos_daili/internal/spec"
)

func pngBytes(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for x := 0; x < size; x++ {
		for y := 0; y < size; y++ {
			img.Set(x, y, color.RGBA{R: 30, G: 120, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testOptions(t *testing.T) BuildOptions {
	t.Helper()
	s, err := spec.NormalizeProxySpec(spec.ProxySpec{
		AppName:       "proxy.home.9f3a",
		DisplayName:   "家里的 Home Assistant",
		Description:   "通过代理访问 192.168.1.10:8123",
		TargetURL:     "192.168.1.10:8123",
		Mode:          "reverse",
		AllUsers:      true,
		InstallVolume: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return BuildOptions{
		Spec: s,
		Assets: Assets{
			ProxyBinary: []byte("#!/bin/sh\nexit 0\n"),
			Icon64:      pngBytes(t, 64),
			Icon256:     pngBytes(t, 256),
		},
		Platform: "x86",
	}
}

func readTar(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = b
	}
	return out
}

func TestBuildProxyLayout(t *testing.T) {
	var buf bytes.Buffer
	res, err := BuildProxy(&buf, testOptions(t))
	if err != nil {
		t.Fatalf("BuildProxy() error: %v", err)
	}
	if res.Size != int64(buf.Len()) {
		t.Fatalf("Size = %d, len = %d", res.Size, buf.Len())
	}
	if res.SHA256 == "" || res.Manifest == "" {
		t.Fatalf("missing result fields: %+v", res)
	}

	files := readTar(t, buf.Bytes())
	for _, name := range []string{
		"manifest", "ICON.PNG", "ICON_256.PNG", "config/privilege",
		"config/resource", "app.tgz", "cmd/main", "cmd/install_init",
		"cmd/install_callback", "cmd/uninstall_init", "cmd/uninstall_callback",
		"cmd/upgrade_init", "cmd/upgrade_callback", "cmd/config_init",
		"cmd/config_callback", "wizard/",
	} {
		if _, ok := files[name]; !ok {
			t.Errorf("missing outer member %q", name)
		}
	}
	if string(files["config/privilege"]) != privilegeJSON {
		t.Errorf("privilege = %s", files["config/privilege"])
	}
	if string(files["config/resource"]) != "{}" {
		t.Errorf("resource = %s", files["config/resource"])
	}

	manifest := string(files["manifest"])
	for _, want := range []string{
		"appname=proxy.home.9f3a",
		"version=1.0.0",
		"display_name=家里的 Home Assistant",
		"desc=通过代理访问 192.168.1.10:8123",
		"platform=x86",
		"desktop_uidir=ui",
		"desktop_applaunchname=proxy.home.9f3a.main",
		"checkport=false",
		"checksum=",
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest missing %q:\n%s", want, manifest)
		}
	}
	for _, forbidden := range []string{"service_port=", "install_type=", "arch="} {
		if strings.Contains(manifest, forbidden) {
			t.Errorf("manifest has forbidden %q:\n%s", forbidden, manifest)
		}
	}

	appSum := md5.Sum(files["app.tgz"])
	if !strings.Contains(manifest, "checksum="+hex.EncodeToString(appSum[:])) {
		t.Errorf("manifest checksum does not match app.tgz: %s", manifest)
	}

	app := readTar(t, files["app.tgz"])
	for _, name := range []string{
		"config.json", "bin/proxy", "ui/config",
		"ui/images/icon_64.png", "ui/images/icon_256.png",
	} {
		if _, ok := app[name]; !ok {
			t.Errorf("missing app member %q", name)
		}
	}
	if !bytes.Equal(app["bin/proxy"], []byte("#!/bin/sh\nexit 0\n")) {
		t.Errorf("bin/proxy = %q", app["bin/proxy"])
	}

	var ui struct {
		URL map[string]map[string]any `json:".url"`
	}
	if err := json.Unmarshal(app["ui/config"], &ui); err != nil {
		t.Fatal(err)
	}
	entry, ok := ui.URL["proxy.home.9f3a.main"]
	if !ok {
		t.Fatalf("ui/config key mismatch: %s", app["ui/config"])
	}
	if entry["url"] != "/cgi/ThirdParty/proxy.home.9f3a/index.cgi/" {
		t.Errorf("entry url = %v", entry["url"])
	}
	if entry["allUsers"] != true || entry["noDisplay"] != false {
		t.Errorf("entry visibility = %v/%v", entry["allUsers"], entry["noDisplay"])
	}
	if entry["icon"] != "images/icon_{0}.png" {
		t.Errorf("icon = %v", entry["icon"])
	}

	var cfg map[string]any
	if err := json.Unmarshal(app["config.json"], &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["schema"] != float64(1) || cfg["appname"] != "proxy.home.9f3a" || cfg["mode"] != "reverse" {
		t.Errorf("config identity = %+v", cfg)
	}
	if cfg["target"] != "http://192.168.1.10:8123" {
		t.Errorf("config target = %v", cfg["target"])
	}
	if cfg["tls"] == nil || cfg["guard"] == nil {
		t.Errorf("config missing tls/guard: %+v", cfg)
	}

	if !bytes.Contains(files["cmd/install_callback"], []byte("${TRIM_PKGVAR}/proxy.sock")) {
		t.Errorf("install_callback socket = %s", files["cmd/install_callback"])
	}
	if !bytes.Contains(files["cmd/main"], []byte("SOCK=\"$VAR/proxy.sock\"")) || !bytes.Contains(files["cmd/main"], []byte("--socket \"$SOCK\"")) {
		t.Errorf("main socket = %s", files["cmd/main"])
	}
}

func TestBuildProxyDirectConfig(t *testing.T) {
	opts := testOptions(t)
	opts.Spec.Mode = "direct"
	opts.Spec.AllUsers = false
	var buf bytes.Buffer
	_, err := BuildProxy(&buf, opts)
	if err != nil {
		t.Fatalf("BuildProxy() error: %v", err)
	}
	files := readTar(t, buf.Bytes())
	app := readTar(t, files["app.tgz"])
	var cfg map[string]any
	if err := json.Unmarshal(app["config.json"], &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["mode"] != "direct" {
		t.Errorf("mode = %v", cfg["mode"])
	}
	for _, key := range []string{"tls", "guard", "basic_auth"} {
		if _, ok := cfg[key]; ok {
			t.Errorf("direct config should omit %s: %+v", key, cfg)
		}
	}
}

func TestBuildProxyDeterministic(t *testing.T) {
	opts := testOptions(t)
	var a, b bytes.Buffer
	if _, err := BuildProxy(&a, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildProxy(&b, opts); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		h1 := sha256.Sum256(a.Bytes())
		h2 := sha256.Sum256(b.Bytes())
		t.Fatalf("non-deterministic package: %x != %x", h1, h2)
	}
}

func TestBuildProxyRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*BuildOptions)
	}{
		{"missing binary", func(o *BuildOptions) { o.Assets.ProxyBinary = nil }},
		{"bad icon 64", func(o *BuildOptions) { o.Assets.Icon64 = []byte("not png") }},
		{"wrong icon size", func(o *BuildOptions) { o.Assets.Icon256 = pngBytes(t, 128) }},
		{"invalid spec", func(o *BuildOptions) { o.Spec.AppName = "-bad" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := testOptions(t)
			tt.mut(&opts)
			var buf bytes.Buffer
			if _, err := BuildProxy(&buf, opts); err == nil {
				t.Fatalf("BuildProxy() succeeded, want error")
			}
		})
	}
}
