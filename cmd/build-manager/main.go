// Command build-manager builds the daili.manager fpk from cmd/manager and
// cmd/proxy, embedding both binaries and the default icons.
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
)

func main() {
	version := flag.String("version", "1.0.0", "package version")
	platform := flag.String("platform", "x86", "x86 (linux/amd64) or arm (linux/arm64)")
	managerBin := flag.String("manager-bin", "", "use an already built manager binary")
	proxyBin := flag.String("proxy-bin", "", "use an already built proxy binary")
	out := flag.String("out", "", "output fpk path (defaults to dist/daili.manager-<version>.fpk)")
	flag.Parse()
	if *platform != "x86" && *platform != "arm" {
		fmt.Fprintln(os.Stderr, "build-manager: --platform must be x86 or arm")
		os.Exit(2)
	}
	if *out == "" {
		*out = filepath.Join("dist", "daili.manager-"+*version+".fpk")
	}
	manager, err := loadBin(*managerBin, "./cmd/manager", *platform)
	if err != nil {
		fail(err)
	}
	proxy, err := loadBin(*proxyBin, "./cmd/proxy", *platform)
	if err != nil {
		fail(err)
	}
	var pkg bytes.Buffer
	built, err := fpk.BuildManager(&pkg, fpk.ManagerOptions{
		ManagerBinary: manager,
		ProxyBinary:   proxy,
		Icon64:        assets.Icon64,
		Icon256:       assets.Icon256,
		Version:       *version,
		Platform:      *platform,
	})
	if err != nil {
		fail(err)
	}
	checked, err := fpk.VerifyManager(pkg.Bytes())
	if err != nil {
		fail(fmt.Errorf("generated manager package failed verification: %w", err))
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
	fmt.Printf("manager:  %d bytes (static ELF64)\n", checked.ManagerSize)
	fmt.Printf("proxy:    %d bytes (static ELF64)\n", checked.ProxySize)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "build-manager:", err)
	os.Exit(1)
}

func loadBin(path, pkg, platform string) ([]byte, error) {
	if path != "" {
		return os.ReadFile(path)
	}
	dir, err := os.MkdirTemp("", "daili-bin-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "bin")
	goarch := "amd64"
	if platform == "arm" {
		goarch = "arm64"
	}
	goBin := os.Getenv("GO")
	if goBin == "" {
		goBin = "go"
	}
	cmd := exec.Command(goBin, "build", "-trimpath", "-ldflags", "-s -w -buildid=", "-o", bin, pkg)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = buildEnv("linux", goarch)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("cross-compile %s: %w", pkg, err)
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
