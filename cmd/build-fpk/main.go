// Command build-fpk builds one installable proxy fpk from cmd/proxy and the
// embedded default icons.
//
// Typical use from the repository root:
//
//	go run ./cmd/build-fpk --appname proxy.home.9f3a \
//	  --display-name "家里的 Home Assistant" --target http://192.168.1.10:8123 \
//	  --out dist/proxy.home.9f3a-1.0.0.fpk
//
// The tool cross-compiles cmd/proxy for linux/amd64 (or linux/arm64 with
// --platform arm) as a static binary, builds the package, verifies its
// structure, and only then moves it into place.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jeefyang/fnos_daili/internal/assets"
	"github.com/jeefyang/fnos_daili/internal/fpk"
	"github.com/jeefyang/fnos_daili/internal/spec"
)

func main() {
	var (
		appName       = flag.String("appname", "", "application id (3-32 bytes, e.g. proxy.home.9f3a)")
		displayName   = flag.String("display-name", "", "display name shown in the fnOS desktop")
		desc          = flag.String("desc", "", "optional description (defaults to the target URL)")
		targetURL     = flag.String("target", "", "target origin, e.g. http://192.168.1.10:8123")
		mode          = flag.String("mode", "reverse", "reverse or direct")
		version       = flag.String("version", "1.0.0", "package version")
		entryTitle    = flag.String("entry-title", "", "desktop entry title (defaults to display-name)")
		uiType        = flag.String("ui-type", "url", "url or iframe")
		allUsers      = flag.Bool("all-users", false, "show the entry to all users")
		noDisplay     = flag.Bool("no-display", false, "hide the desktop entry")
		fileTypes     = flag.String("file-types", "", "comma-separated file types")
		skipTLS       = flag.Bool("skip-tls-verify", false, "do not verify the target TLS certificate")
		allowLoopback = flag.Bool("allow-loopback", false, "allow a loopback target (reverse mode only)")
		basicUser     = flag.String("basic-auth-user", "", "optional HTTP Basic Auth username")
		basicHash     = flag.String("basic-auth-hash", "", "bcrypt cost-12 password hash for Basic Auth")
		platform      = flag.String("platform", "x86", "x86 (linux/amd64) or arm (linux/arm64)")
		proxyBin      = flag.String("proxy-bin", "", "use an already built proxy binary instead of cross-compiling")
		out           = flag.String("out", "", "output fpk path (defaults to dist/<appname>-<version>.fpk)")
	)
	flag.Parse()

	if *appName == "" || *displayName == "" || *targetURL == "" {
		flag.Usage()
		fmt.Fprintln(os.Stderr, "build-fpk: --appname, --display-name and --target are required")
		os.Exit(2)
	}
	if (*basicUser == "") != (*basicHash == "") {
		fmt.Fprintln(os.Stderr, "build-fpk: --basic-auth-user and --basic-auth-hash must be set together")
		os.Exit(2)
	}
	if *platform != "x86" && *platform != "arm" {
		fmt.Fprintln(os.Stderr, "build-fpk: --platform must be x86 or arm")
		os.Exit(2)
	}
	if *out == "" {
		*out = filepath.Join("dist", *appName+"-"+*version+".fpk")
	}

	proxy, err := loadProxy(*proxyBin, *platform)
	if err != nil {
		fail(err)
	}
	var auth *spec.BasicAuth
	if *basicUser != "" {
		auth = &spec.BasicAuth{Username: *basicUser, PasswordHash: *basicHash}
	}
	proxySpec := spec.ProxySpec{
		AppName:       *appName,
		DisplayName:   *displayName,
		Description:   *desc,
		Version:       *version,
		TargetURL:     *targetURL,
		Mode:          *mode,
		EntryTitle:    *entryTitle,
		UIType:        *uiType,
		AllUsers:      *allUsers,
		NoDisplay:     *noDisplay,
		FileTypes:     splitList(*fileTypes),
		SkipTLSVerify: *skipTLS,
		AllowLoopback: *allowLoopback,
		BasicAuth:     auth,
	}
	proxySpec, err = spec.NormalizeProxySpec(proxySpec)
	if err != nil {
		fail(err)
	}

	var pkg bytes.Buffer
	built, err := fpk.BuildProxy(&pkg, fpk.BuildOptions{
		Spec:     proxySpec,
		Assets:   fpk.Assets{ProxyBinary: proxy, Icon64: assets.Icon64, Icon256: assets.Icon256},
		Platform: *platform,
	})
	if err != nil {
		fail(err)
	}
	checked, err := fpk.Verify(pkg.Bytes())
	if err != nil {
		fail(fmt.Errorf("generated package failed verification: %w", err))
	}
	if built.SHA256 != checked.SHA256 || built.Size != checked.Size {
		fail(fmt.Errorf("builder and verifier disagree: %s/%d vs %s/%d", built.SHA256, built.Size, checked.SHA256, checked.Size))
	}
	if err := writeFileAtomic(*out, pkg.Bytes(), 0o600); err != nil {
		fail(err)
	}

	fmt.Printf("package:  %s\n", *out)
	fmt.Printf("appname:  %s\n", checked.AppName)
	fmt.Printf("version:  %s\n", checked.Version)
	fmt.Printf("platform: %s\n", checked.Platform)
	fmt.Printf("size:     %d bytes\n", checked.Size)
	fmt.Printf("sha256:   %s\n", checked.SHA256)
	fmt.Printf("app.tgz:  md5 %s\n", checked.AppTGZChecksum)
	fmt.Printf("proxy:    %d bytes (static ELF64)\n", checked.ProxySize)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "build-fpk:", err)
	os.Exit(1)
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func loadProxy(path, platform string) ([]byte, error) {
	if path != "" {
		return os.ReadFile(path)
	}
	dir, err := os.MkdirTemp("", "daili-proxy-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "proxy")
	goarch := "amd64"
	if platform == "arm" {
		goarch = "arm64"
	}
	goBin := os.Getenv("GO")
	if goBin == "" {
		goBin = "go"
	}
	cmd := exec.Command(goBin, "build", "-trimpath", "-ldflags", "-s -w -buildid=", "-o", bin, "./cmd/proxy")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = buildEnv("linux", goarch)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("cross-compile cmd/proxy: %w", err)
	}
	return os.ReadFile(bin)
}

func buildEnv(goos, goarch string) []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GOOS=") || strings.HasPrefix(kv, "GOARCH=") || strings.HasPrefix(kv, "CGO_ENABLED=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".fpk-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmp)
		}
	}()
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}
