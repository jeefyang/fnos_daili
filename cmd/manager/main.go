package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/cgi"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jeefyang/fnos_daili/internal/assets"
	"github.com/jeefyang/fnos_daili/internal/manager"
	"github.com/jeefyang/fnos_daili/internal/proxy"
)

func main() {
	mode := flag.String("mode", "serve", "serve or cgi")
	socket := flag.String("socket", "", "unix socket path")
	flag.Parse()

	logger, closeLog, err := openLogger()
	if err != nil {
		log.Fatalf("manager: %v", err)
	}
	defer closeLog()

	switch *mode {
	case "serve":
		if err := runServe(*socket, logger); err != nil {
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

func runServe(socket string, logger *log.Logger) error {
	varDir := os.Getenv("TRIM_PKGVAR")
	deskDir := os.Getenv("TRIM_APPDEST")
	if varDir == "" || deskDir == "" {
		return errors.New("TRIM_PKGVAR and TRIM_APPDEST are required")
	}
	if socket == "" {
		socket = filepath.Join(varDir, "manager.sock")
	}
	proxyBin, err := os.ReadFile(filepath.Join(deskDir, "bin", "proxy"))
	if err != nil {
		return fmt.Errorf("read bundled proxy binary: %w", err)
	}
	svc, err := manager.New(manager.Config{
		DataDir:     varDir,
		DeskDir:     deskDir,
		Version:     "1.0.0",
		Platform:    "x86",
		ProxyBinary: proxyBin,
		Icon64:      assets.Icon64,
		Icon256:     assets.Icon256,
		Logger:      logger,
	})
	if err != nil {
		return err
	}
	running, err := prepareSocket(socket)
	if err != nil {
		return err
	}
	if running {
		logger.Printf("manager already running socket=%s", socket)
		return nil
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}
	pidPath := filepath.Join(varDir, "manager.pid")
	pid := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(pidPath, []byte(pid), 0o600); err != nil {
		return err
	}
	defer func() {
		if b, err := os.ReadFile(pidPath); err == nil && strings.TrimSpace(string(b)) == pid {
			_ = os.Remove(pidPath)
		}
	}()
	ui := manager.UI()
	handler := svc.Handler(http.FileServer(http.FS(ui)))
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
	reconcileCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	svc.Reconcile(reconcileCtx)
	cancel()
	logger.Printf("started app=daili.manager socket=%s cli=%s", socket, svc.CLIPath())
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runCGI(socket string, logger *log.Logger) error {
	if socket == "" {
		socket = filepath.Join(os.Getenv("TRIM_PKGVAR"), "manager.sock")
	}
	handler, err := proxy.NewCGI(socket, proxy.Options{Logger: logger})
	if err != nil {
		return err
	}
	return cgi.Serve(handler)
}

// prepareSocket returns true when a live manager already owns the socket. A
// dead socket is removed so this process can bind it.
func prepareSocket(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return true, nil
	}
	return false, os.Remove(path)
}
