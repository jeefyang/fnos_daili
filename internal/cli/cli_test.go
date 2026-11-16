package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	calls []string
	fn    func(args []string) (Result, error)
}

func (f *fakeRunner) Run(ctx context.Context, dir string, args ...string) (Result, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	return f.fn(args)
}

func TestParseListBordered(t *testing.T) {
	out := "" +
		"+------------+--------------+---------+---------+-----------------+\n" +
		"| APP NAME   | DISPLAY NAME | VERSION | STATUS  | DEPENDENCY APPS |\n" +
		"+------------+--------------+---------+---------+-----------------+\n" +
		"| daili.demo | 示例代理     | 1.0.0   | running |                 |\n" +
		"+------------+--------------+---------+---------+-----------------+\n"
	apps, err := ParseList(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 {
		t.Fatalf("apps = %+v", apps)
	}
	if apps[0].AppName != "daili.demo" || apps[0].Version != "1.0.0" || apps[0].Status != "running" {
		t.Fatalf("app = %+v", apps[0])
	}
}

func TestParseListUnicodeBorder(t *testing.T) {
	out := "┌────────────┬──────────────┬─────────┬─────────┐\n" +
		"│ APP NAME   │ DISPLAY NAME │ VERSION │ STATUS  │\n" +
		"├────────────┼──────────────┼─────────┼─────────┤\n" +
		"│ daili.demo │ Demo         │ 1.0.0   │ stopped │\n" +
		"└────────────┴──────────────┴─────────┴─────────┘\n"
	apps, err := ParseList(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Status != "stopped" {
		t.Fatalf("apps = %+v", apps)
	}
}

func TestParseListEmptyTableIsEmptyList(t *testing.T) {
	apps, err := ParseList("| APP NAME | DISPLAY NAME | VERSION | STATUS |\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 0 {
		t.Fatalf("apps = %+v", apps)
	}
}

func TestInstallRequiresMarker(t *testing.T) {
	r := &fakeRunner{fn: func(args []string) (Result, error) {
		return Result{Stdout: "[Info]Installation complete.\n", ExitCode: 0}, nil
	}}
	c := &Client{Path: "/usr/local/bin/appcenter-cli", Runner: r}
	if _, err := c.Install(context.Background(), "1", "/tmp/a.fpk"); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 || r.calls[0] != "install-fpk -v 1 /tmp/a.fpk" {
		t.Fatalf("calls = %v", r.calls)
	}
}

func TestInstallRejectsErrorOutput(t *testing.T) {
	r := &fakeRunner{fn: func(args []string) (Result, error) {
		return Result{Stdout: "[Info]Installation complete\n[Error]boom\n", ExitCode: 0}, nil
	}}
	c := &Client{Path: "appcenter-cli", Runner: r}
	_, err := c.Install(context.Background(), "1", "/tmp/a.fpk")
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != "cli_install_failed" {
		t.Fatalf("err = %v", err)
	}
}

func TestInstallAlreadyInstalled(t *testing.T) {
	r := &fakeRunner{fn: func(args []string) (Result, error) {
		return Result{Stdout: "[Info]Application [daili.demo] is installed.\n"}, nil
	}}
	c := &Client{Path: "appcenter-cli", Runner: r}
	_, err := c.Install(context.Background(), "1", "/tmp/a.fpk")
	if !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("err = %v", err)
	}
}

func TestUninstallRequiresMarker(t *testing.T) {
	r := &fakeRunner{fn: func(args []string) (Result, error) {
		return Result{Stdout: "Application [daili.demo] uninstall success\n"}, nil
	}}
	c := &Client{Path: "appcenter-cli", Runner: r}
	if _, err := c.Uninstall(context.Background(), "daili.demo"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitRunningPollsList(t *testing.T) {
	n := 0
	r := &fakeRunner{fn: func(args []string) (Result, error) {
		n++
		status := "starting"
		if n >= 3 {
			status = "running"
		}
		return Result{Stdout: "| APP NAME | DISPLAY NAME | VERSION | STATUS |\n| daili.demo | Demo | 1.0.0 | " + status + " |\n"}, nil
	}}
	c := &Client{Path: "appcenter-cli", Runner: r, PollEvery: time.Millisecond}
	st, err := c.WaitRunning(context.Background(), "daili.demo")
	if err != nil || st != "running" {
		t.Fatalf("WaitRunning() = %q, %v", st, err)
	}
}
