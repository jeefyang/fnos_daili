package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cgi"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jeefyang/fnos_daili/internal/proxy"
	"github.com/jeefyang/fnos_daili/internal/proxyconfig"
)

func main() {
	mode := flag.String("mode", "serve", "serve or cgi")
	socket := flag.String("socket", "", "unix socket path")
	configPath := flag.String("config", "", "config.json path")
	flag.Parse()

	logger, closeLog, err := openLogger()
	if err != nil {
		log.Fatalf("proxy: %v", err)
	}
	defer closeLog()

	switch *mode {
	case "serve":
		if err := runServe(*socket, *configPath, logger); err != nil {
			logger.Fatalf("serve: %v", err)
		}
	case "cgi":
		if err := runCGI(*socket, logger); err != nil {
			logger.Fatalf("cgi: %v", err)
		}
	default:
		logger.Fatalf("unknown mode %q", *mode)
	}
}

func runServe(socket, configPath string, logger *log.Logger) error {
	path, err := resolveConfig(configPath)
	if err != nil {
		return err
	}
	if err := checkConfigMode(path); err != nil {
		return err
	}
	cfg, err := proxyconfig.Load(path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if socket == "" {
		socket = filepath.Join(os.Getenv("TRIM_PKGVAR"), "proxy.sock")
	}
	if socket == "" {
		return errors.New("no socket path")
	}
	handler, err := proxy.New(cfg, proxy.Options{Logger: logger})
	if err != nil {
		return err
	}
	if err := prepareSocket(socket); err != nil {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	logger.Printf("started app=%s mode=%s target=%s socket=%s", cfg.AppName, cfg.Mode, cfg.Target, socket)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runCGI(socket string, logger *log.Logger) error {
	if socket == "" {
		socket = filepath.Join(os.Getenv("TRIM_PKGVAR"), "proxy.sock")
	}
	handler, err := proxy.NewCGI(socket, proxy.Options{Logger: logger})
	if err != nil {
		return err
	}
	return cgi.Serve(handler)
}

func resolveConfig(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	for _, dir := range []string{os.Getenv("TRIM_PKGVAR"), os.Getenv("TRIM_APPDEST")} {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, "config.json")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", errors.New("config.json not found")
}

func checkConfigMode(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("config %s must be 0600 (got %04o)", path, info.Mode().Perm())
	}
	return nil
}

func prepareSocket(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("socket %s is already in use", path)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
}

var _ io.Closer = (*rotatingWriter)(nil)
