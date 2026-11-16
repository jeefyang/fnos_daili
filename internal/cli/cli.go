// Package cli wraps appcenter-cli. Exit code 0 is not success: install and
// uninstall are judged by their output markers, and every app lifecycle change
// is confirmed by list/status.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var fallbackPaths = []string{
	"/usr/local/bin/appcenter-cli",
	"/var/apps/appcenter/target/bin/appcenter-cli",
	"/usr/bin/appcenter-cli",
}

// App is one row of appcenter-cli list.
type App struct {
	AppName     string
	DisplayName string
	Version     string
	Status      string
}

// Error is a classified CLI failure safe to show to an operator.
type Error struct {
	Code    string
	Message string
	Output  string
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Is(target error) bool {
	var t *Error
	if errors.As(target, &t) {
		return t.Code == e.Code
	}
	return false
}

var ErrAlreadyInstalled = &Error{Code: "already_installed", Message: "应用已安装"}

// Runner executes one CLI invocation. Tests replace it with a fake.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) (Result, error)
}

// Result is one CLI invocation's output.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Combined returns stdout and stderr in a stable order.
func (r Result) Combined() string { return r.Stdout + r.Stderr }

// Client is an appcenter-cli installation.
type Client struct {
	Path      string
	Runner    Runner
	PollEvery time.Duration
}

// Locate finds appcenter-cli in the standard locations and then PATH.
func Locate() (string, error) {
	for _, p := range fallbackPaths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	if p, err := exec.LookPath("appcenter-cli"); err == nil {
		return p, nil
	}
	return "", &Error{Code: "cli_not_found", Message: "找不到 appcenter-cli"}
}

// New returns a client. An empty path is located lazily.
func New(path string) *Client {
	if path == "" {
		path, _ = Locate()
	}
	return &Client{Path: path, Runner: execRunner{path: path}, PollEvery: 2 * time.Second}
}

func (c *Client) pollEvery() time.Duration {
	if c.PollEvery <= 0 {
		return 2 * time.Second
	}
	return c.PollEvery
}

func (c *Client) run(ctx context.Context, dir string, args ...string) (Result, error) {
	if c.Path == "" {
		return Result{}, &Error{Code: "cli_not_found", Message: "找不到 appcenter-cli"}
	}
	return c.Runner.Run(ctx, dir, args...)
}

// List returns the installed applications. A parse failure is an error, never
// an empty list, so reconciliation does not silently mark everything missing.
func (c *Client) List(ctx context.Context) ([]App, error) {
	res, err := c.run(ctx, "", "list")
	if err != nil {
		return nil, &Error{Code: "cli_list_failed", Message: "appcenter-cli list 执行失败", Output: res.Combined()}
	}
	apps, err := ParseList(res.Combined())
	if err != nil {
		return nil, &Error{Code: "cli_list_parse", Message: err.Error(), Output: res.Combined()}
	}
	return apps, nil
}

var ansiRE = regexp.MustCompile("\\x1b\\[[0-9;]*[A-Za-z]")

// ParseList parses the bordered table emitted by appcenter-cli list. It also
// accepts whitespace-separated rows from older builds.
func ParseList(out string) ([]App, error) {
	var apps []App
	seenHeader := false
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(ansiRE.ReplaceAllString(raw, ""))
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		if strings.Contains(upper, "APP NAME") && strings.Contains(upper, "STATUS") {
			seenHeader = true
			continue
		}
		if !seenHeader && !strings.ContainsAny(line, "|") {
			continue
		}
		fields := splitRow(line)
		if len(fields) < 4 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if !looksLikeAppName(name) {
			continue
		}
		apps = append(apps, App{
			AppName:     name,
			DisplayName: strings.TrimSpace(fields[1]),
			Version:     strings.TrimSpace(fields[2]),
			Status:      strings.ToLower(strings.TrimSpace(fields[3])),
		})
	}
	if len(apps) == 0 && !seenHeader && strings.Contains(out, "APP NAME") {
		return nil, fmt.Errorf("list table had an unparsable header")
	}
	return apps, nil
}

func splitRow(line string) []string {
	line = strings.ReplaceAll(line, "│", "|")
	if strings.Contains(line, "|") {
		parts := strings.Split(line, "|")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return strings.Fields(line)
}

var appNameRE = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9._-]*$")

func looksLikeAppName(s string) bool {
	return len(s) >= 3 && len(s) <= 64 && appNameRE.MatchString(s)
}

// Status queries one app and parses the state word. appcenter-cli writes status
// to stderr on some versions; both streams are inspected.
func (c *Client) Status(ctx context.Context, appname string) (string, error) {
	res, err := c.run(ctx, "", "status", appname)
	if err != nil {
		return "", &Error{Code: "cli_status_failed", Message: "appcenter-cli status 执行失败", Output: res.Combined()}
	}
	text := strings.ToLower(ansiRE.ReplaceAllString(res.Combined(), ""))
	for _, st := range []string{"running", "starting", "stopping", "stopped", "noinstall", "installed", "uninstalling"} {
		if strings.Contains(text, st) {
			return st, nil
		}
	}
	return "", &Error{Code: "cli_status_unknown", Message: "无法识别应用状态", Output: res.Combined()}
}

// Find returns one app from list.
func (c *Client) Find(ctx context.Context, appname string) (*App, error) {
	apps, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range apps {
		if apps[i].AppName == appname {
			return &apps[i], nil
		}
	}
	return nil, nil
}

// Install runs install-fpk and requires the success marker.
func (c *Client) Install(ctx context.Context, volume, pkgPath string) (Result, error) {
	if volume == "" {
		volume = "1"
	}
	res, err := c.run(ctx, filepath.Dir(pkgPath), "install-fpk", "-v", volume, pkgPath)
	if err != nil {
		return res, &Error{Code: "cli_install_failed", Message: "install-fpk 执行失败", Output: res.Combined()}
	}
	text := res.Combined()
	if strings.Contains(text, "is installed") {
		return res, ErrAlreadyInstalled
	}
	if !strings.Contains(text, "Installation complete") {
		return res, &Error{Code: "cli_install_failed", Message: "安装未完成：" + shortError(text), Output: text}
	}
	if strings.Contains(text, "[Error]") {
		return res, &Error{Code: "cli_install_failed", Message: "安装报错：" + shortError(text), Output: text}
	}
	return res, nil
}

// Uninstall runs uninstall and requires the success marker.
func (c *Client) Uninstall(ctx context.Context, appname string) (Result, error) {
	res, err := c.run(ctx, "", "uninstall", appname)
	if err != nil {
		return res, &Error{Code: "cli_uninstall_failed", Message: "uninstall 执行失败", Output: res.Combined()}
	}
	text := res.Combined()
	if strings.Contains(text, "uninstall success") {
		return res, nil
	}
	return res, &Error{Code: "cli_uninstall_failed", Message: "卸载失败：" + shortError(text), Output: text}
}

// Start starts one app and requires the launch marker.
func (c *Client) Start(ctx context.Context, appname string) error {
	res, err := c.run(ctx, "", "start", appname)
	if err != nil {
		return &Error{Code: "cli_start_failed", Message: "start 执行失败", Output: res.Combined()}
	}
	text := res.Combined()
	if strings.Contains(text, "Launching complete") || strings.Contains(text, "start success") {
		return nil
	}
	return &Error{Code: "cli_start_failed", Message: "启动失败：" + shortError(text), Output: text}
}

// Stop stops one app.
func (c *Client) Stop(ctx context.Context, appname string) error {
	res, err := c.run(ctx, "", "stop", appname)
	if err != nil {
		return &Error{Code: "cli_stop_failed", Message: "stop 执行失败", Output: res.Combined()}
	}
	text := res.Combined()
	if strings.Contains(text, "stop success") || strings.Contains(text, "stopping") {
		return nil
	}
	return &Error{Code: "cli_stop_failed", Message: "停止失败：" + shortError(text), Output: text}
}

// DefaultVolume returns appcenter-cli default-volume, falling back to 1.
func (c *Client) DefaultVolume(ctx context.Context) string {
	res, err := c.run(ctx, "", "default-volume")
	if err != nil {
		return "1"
	}
	for _, line := range strings.Split(res.Combined(), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.ContainsAny(line, " \t") {
			return line
		}
	}
	return "1"
}

// WaitRunning polls list until the app reaches a terminal status or the
// context ends. It returns the last observed status.
func (c *Client) WaitRunning(ctx context.Context, appname string) (string, error) {
	last := "unknown"
	for {
		if app, err := c.Find(ctx, appname); err == nil && app != nil {
			last = app.Status
			switch app.Status {
			case "running":
				return last, nil
			case "stopped":
				return last, nil
			}
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(c.pollEvery()):
		}
	}
}

// WaitGone polls list until the app disappears.
func (c *Client) WaitGone(ctx context.Context, appname string) error {
	for {
		app, err := c.Find(ctx, appname)
		if err == nil && app == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.pollEvery()):
		}
	}
}

func shortError(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(ansiRE.ReplaceAllString(line, ""))
		if line == "" {
			continue
		}
		if i := strings.Index(line, "[Error]"); i >= 0 {
			line = line[i:]
		}
		if len(line) > 180 {
			line = line[:180]
		}
		return line
	}
	return "appcenter-cli 返回失败"
}

type execRunner struct{ path string }

func (r execRunner) Run(ctx context.Context, dir string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, r.path, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	return res, err
}
