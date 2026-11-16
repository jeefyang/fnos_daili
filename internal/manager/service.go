// Package manager implements the daili.manager service: target inventory,
// package generation, appcenter-cli lifecycle, and the embedded web UI.
package manager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/jeefyang/fnos_daili/internal/cli"
	"github.com/jeefyang/fnos_daili/internal/spec"
	"github.com/jeefyang/fnos_daili/internal/state"
)

// Config wires the manager to its environment.
type Config struct {
	DataDir     string
	DeskDir     string
	Version     string
	Platform    string
	CLIPath     string
	ProxyBinary []byte
	Icon64      []byte
	Icon256     []byte
	Logger      *log.Logger
	Now         func() time.Time
	PollEvery   time.Duration
}

// Service owns state, operations, and the CLI client.
type Service struct {
	cfg     Config
	store   state.Store
	cli     *cli.Client
	logger  *log.Logger
	logPath string
	token   string

	mu        sync.Mutex
	opMu      sync.Mutex
	st        state.State
	unmanaged []UnmanagedApp
	running   map[string]bool
	ops       map[string]string
	done      map[string]state.Operation
}

// UnmanagedApp is an installed app that is not in state but has a package dir.
type UnmanagedApp struct {
	AppName     string "json:\"appname\""
	DisplayName string "json:\"display_name\""
	Version     string "json:\"version\""
	Status      string "json:\"status\""
}

// AuthInput carries a plaintext password on create/update. It is never stored.
type AuthInput struct {
	Username string "json:\"username\""
	Password string "json:\"password\""
}

// SpecInput is the API's editable portion of a target.
type SpecInput struct {
	AppName       string     "json:\"appname\""
	DisplayName   string     "json:\"display_name\""
	Description   string     "json:\"description\""
	Version       string     "json:\"version\""
	TargetURL     string     "json:\"target_url\""
	Mode          string     "json:\"mode\""
	EntryTitle    string     "json:\"entry_title\""
	UIType        string     "json:\"ui_type\""
	AllUsers      bool       "json:\"all_users\""
	NoDisplay     bool       "json:\"no_display\""
	FileTypes     []string   "json:\"file_types\""
	InstallVolume string     "json:\"install_volume\""
	SkipTLSVerify bool       "json:\"skip_tls_verify\""
	AllowLoopback bool       "json:\"allow_loopback\""
	BasicAuth     *AuthInput "json:\"basic_auth,omitempty\""
}

// AuthView never contains a password or hash.
type AuthView struct {
	Username    string "json:\"username\""
	HasPassword bool   "json:\"has_password\""
}

// SpecView is the API-safe projection of spec.ProxySpec.
type SpecView struct {
	ID            string    "json:\"id\""
	AppName       string    "json:\"appname\""
	DisplayName   string    "json:\"display_name\""
	Description   string    "json:\"description\""
	Version       string    "json:\"version\""
	TargetURL     string    "json:\"target_url\""
	Mode          string    "json:\"mode\""
	EntryTitle    string    "json:\"entry_title\""
	UIType        string    "json:\"ui_type\""
	Port          int       "json:\"port\""
	AllUsers      bool      "json:\"all_users\""
	NoDisplay     bool      "json:\"no_display\""
	FileTypes     []string  "json:\"file_types\""
	InstallVolume string    "json:\"install_volume\""
	SkipTLSVerify bool      "json:\"skip_tls_verify\""
	AllowLoopback bool      "json:\"allow_loopback\""
	BasicAuth     *AuthView "json:\"basic_auth,omitempty\""
}

// TargetView is one list row.
type TargetView struct {
	Spec    SpecView      "json:\"spec\""
	Runtime state.Runtime "json:\"runtime\""
}

// ListResult is GET /api/targets.
type ListResult struct {
	Targets   []TargetView   "json:\"targets\""
	Unmanaged []UnmanagedApp "json:\"unmanaged\""
	CLIError  string         "json:\"cli_error,omitempty\""
}

// New loads state and prepares the data directory. It does not call the CLI.
func New(cfg Config) (*Service, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("manager: DataDir is required")
	}
	if cfg.Version == "" {
		cfg.Version = "1.0.0"
	}
	if cfg.Platform == "" {
		cfg.Platform = "x86"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(io.Discard, "", 0)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	for _, d := range []string{"packages", "build", "logs"} {
		if err := os.MkdirAll(filepath.Join(cfg.DataDir, d), 0o755); err != nil {
			return nil, err
		}
	}
	store := state.Store{Path: filepath.Join(cfg.DataDir, "state.json")}
	st, warning, err := store.Load()
	if err != nil {
		return nil, err
	}
	if warning != "" {
		cfg.Logger.Printf("state: %s", warning)
	}
	token, err := loadOrCreateToken(filepath.Join(cfg.DataDir, "csrf.token"))
	if err != nil {
		return nil, err
	}
	client := cli.New(cfg.CLIPath)
	if cfg.PollEvery > 0 {
		client.PollEvery = cfg.PollEvery
	}
	return &Service{
		cfg:     cfg,
		store:   store,
		cli:     client,
		logger:  cfg.Logger,
		logPath: filepath.Join(cfg.DataDir, "manager.log"),
		token:   token,
		st:      st,
		running: map[string]bool{},
		ops:     map[string]string{},
		done:    map[string]state.Operation{},
	}, nil
}

// Token returns the CSRF token embedded into the page.
func (s *Service) Token() string { return s.token }

// DataDir returns the manager data directory.
func (s *Service) DataDir() string { return s.cfg.DataDir }

// Reconcile refreshes runtime state from appcenter-cli at startup.
func (s *Service) Reconcile(ctx context.Context) { s.reconcile(ctx, true) }

// CLIPath returns the located appcenter-cli path, if any.
func (s *Service) CLIPath() string { return s.cli.Path }

func (s *Service) now() time.Time { return s.cfg.Now().UTC() }

func (s *Service) saveLocked() error { return s.store.Save(s.st) }

func (s *Service) findLocked(id string) *state.Target {
	for i := range s.st.Targets {
		if s.st.Targets[i].Spec.ID == id {
			return &s.st.Targets[i]
		}
	}
	return nil
}

func (s *Service) byAppLocked(appname string) *state.Target {
	for i := range s.st.Targets {
		if s.st.Targets[i].Spec.AppName == appname {
			return &s.st.Targets[i]
		}
	}
	return nil
}

var (
	errNotFound = errors.New("target not found")
	errConflict = errors.New("operation already running")
)

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

var hexTokenRE = regexp.MustCompile("^[0-9a-f]{64}$")

func loadOrCreateToken(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		t := strings.TrimSpace(string(b))
		if hexTokenRE.MatchString(t) {
			_ = os.Chmod(path, 0o600)
			return t, nil
		}
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	t := hex.EncodeToString(b[:])
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(t+"\n"), 0o600); err != nil {
		return "", err
	}
	return t, nil
}

// Create validates and stores a new draft target.
func (s *Service) Create(in SpecInput) (TargetView, error) {
	sp, err := s.specFromInput(in, nil)
	if err != nil {
		return TargetView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byAppLocked(sp.AppName) != nil {
		return TargetView{}, &spec.Error{Field: "appname", Message: "already exists"}
	}
	sp.ID = newID()
	port, err := s.allocatePortLocked()
	if err != nil {
		return TargetView{}, err
	}
	sp.Port = port
	now := s.now()
	t := state.Target{
		Spec: sp,
		Runtime: state.Runtime{
			State:     state.StateDraft,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	s.st.Targets = append(s.st.Targets, t)
	if err := s.saveLocked(); err != nil {
		return TargetView{}, err
	}
	return viewOf(t), nil
}

// Update replaces the editable spec. appname is immutable.
func (s *Service) Update(id string, in SpecInput) (TargetView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.findLocked(id)
	if t == nil {
		return TargetView{}, errNotFound
	}
	if in.AppName != "" && in.AppName != t.Spec.AppName {
		return TargetView{}, &spec.Error{Field: "appname", Message: "cannot be changed after creation"}
	}
	in.AppName = t.Spec.AppName
	sp, err := s.specFromInput(in, t.Spec.BasicAuth)
	if err != nil {
		return TargetView{}, err
	}
	sp.ID = t.Spec.ID
	sp.Port = t.Spec.Port
	old := t.Spec
	t.Spec = sp
	if isInstalledState(t.Runtime.State) && !sameInstallInput(old, sp) {
		t.Runtime.NeedsReapply = true
	}
	t.Runtime.UpdatedAt = s.now()
	if err := s.saveLocked(); err != nil {
		return TargetView{}, err
	}
	return viewOf(*t), nil
}

// allocatePortLocked picks the lowest free port in the proxy range. The bind
// test catches listeners that are not in state.json (for example a manually
// installed package).
func (s *Service) allocatePortLocked() (int, error) {
	used := map[int]bool{}
	for _, t := range s.st.Targets {
		used[t.Spec.Port] = true
	}
	for p := spec.PortMin; p <= spec.PortMax; p++ {
		if used[p] {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", p))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return p, nil
	}
	return 0, &spec.Error{Field: "port", Message: fmt.Sprintf("no free port in %d-%d", spec.PortMin, spec.PortMax)}
}

func (s *Service) specFromInput(in SpecInput, existingAuth *spec.BasicAuth) (spec.ProxySpec, error) {
	sp := spec.ProxySpec{
		AppName:       strings.TrimSpace(in.AppName),
		DisplayName:   strings.TrimSpace(in.DisplayName),
		Description:   strings.TrimSpace(in.Description),
		Version:       strings.TrimSpace(in.Version),
		TargetURL:     strings.TrimSpace(in.TargetURL),
		Mode:          strings.TrimSpace(in.Mode),
		EntryTitle:    strings.TrimSpace(in.EntryTitle),
		UIType:        strings.TrimSpace(in.UIType),
		AllUsers:      in.AllUsers,
		NoDisplay:     in.NoDisplay,
		FileTypes:     in.FileTypes,
		InstallVolume: strings.TrimSpace(in.InstallVolume),
		SkipTLSVerify: in.SkipTLSVerify,
		AllowLoopback: in.AllowLoopback,
	}
	if sp.InstallVolume == "" {
		sp.InstallVolume = "1"
	}
	if in.BasicAuth != nil && strings.TrimSpace(in.BasicAuth.Username) != "" {
		username := strings.TrimSpace(in.BasicAuth.Username)
		if len(username) > 64 {
			return sp, &spec.Error{Field: "basic_auth", Message: "username must be 1-64 bytes"}
		}
		if in.BasicAuth.Password == "" {
			if existingAuth == nil || existingAuth.Username != username {
				return sp, &spec.Error{Field: "basic_auth", Message: "password is required"}
			}
			sp.BasicAuth = &spec.BasicAuth{Username: username, PasswordHash: existingAuth.PasswordHash}
		} else {
			if len(in.BasicAuth.Password) < 8 || len(in.BasicAuth.Password) > 72 {
				return sp, &spec.Error{Field: "basic_auth", Message: "password must be 8-72 bytes"}
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(in.BasicAuth.Password), 12)
			if err != nil {
				return sp, err
			}
			sp.BasicAuth = &spec.BasicAuth{Username: username, PasswordHash: string(hash)}
		}
	}
	return spec.NormalizeProxySpec(sp)
}

func sameInstallInput(a, b spec.ProxySpec) bool {
	return a.AppName == b.AppName && a.DisplayName == b.DisplayName &&
		a.Description == b.Description && a.Version == b.Version &&
		a.TargetURL == b.TargetURL && a.Mode == b.Mode && a.EntryTitle == b.EntryTitle &&
		a.UIType == b.UIType && a.AllUsers == b.AllUsers && a.NoDisplay == b.NoDisplay &&
		a.InstallVolume == b.InstallVolume && a.SkipTLSVerify == b.SkipTLSVerify &&
		a.AllowLoopback == b.AllowLoopback && sameAuth(a.BasicAuth, b.BasicAuth)
}

func sameAuth(a, b *spec.BasicAuth) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Username == b.Username && a.PasswordHash == b.PasswordHash
}

func isInstalledState(st string) bool {
	switch st {
	case state.StateRunning, state.StateInstalledNotRunning, state.StateMissing,
		state.StateFailed, state.StateBroken, state.StateUninstallFailed:
		return true
	}
	return false
}

// Get returns one target.
func (s *Service) Get(id string) (TargetView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.findLocked(id)
	if t == nil {
		return TargetView{}, false
	}
	return viewOf(*t), true
}

// List returns all targets plus unmanaged apps.
func (s *Service) List() ListResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := ListResult{Targets: make([]TargetView, 0, len(s.st.Targets)), Unmanaged: append([]UnmanagedApp(nil), s.unmanaged...)}
	for _, t := range s.st.Targets {
		out.Targets = append(out.Targets, viewOf(t))
	}
	return out
}

func viewOf(t state.Target) TargetView {
	sp := SpecView{
		ID: t.Spec.ID, AppName: t.Spec.AppName, DisplayName: t.Spec.DisplayName,
		Description: t.Spec.Description, Version: t.Spec.Version, TargetURL: t.Spec.TargetURL,
		Mode: t.Spec.Mode, EntryTitle: t.Spec.EntryTitle, UIType: t.Spec.UIType,
		Port: t.Spec.Port, AllUsers: t.Spec.AllUsers, NoDisplay: t.Spec.NoDisplay, FileTypes: t.Spec.FileTypes,
		InstallVolume: t.Spec.InstallVolume, SkipTLSVerify: t.Spec.SkipTLSVerify,
		AllowLoopback: t.Spec.AllowLoopback,
	}
	if t.Spec.BasicAuth != nil {
		sp.BasicAuth = &AuthView{Username: t.Spec.BasicAuth.Username, HasPassword: t.Spec.BasicAuth.PasswordHash != ""}
	}
	if sp.FileTypes == nil {
		sp.FileTypes = []string{}
	}
	return TargetView{Spec: sp, Runtime: t.Runtime}
}

// AppNameCheck validates an appname against state and the installed list.
func (s *Service) AppNameCheck(ctx context.Context, name string) (bool, string) {
	if err := spec.ValidateAppName(name); err != nil {
		return false, err.Error()
	}
	s.mu.Lock()
	if s.byAppLocked(name) != nil {
		s.mu.Unlock()
		return false, "appname 已在托管台中"
	}
	s.mu.Unlock()
	if s.cli.Path == "" {
		return true, ""
	}
	app, err := s.cli.Find(ctx, name)
	if err != nil {
		return true, "无法确认是否已安装：" + err.Error()
	}
	if app != nil {
		return false, "系统里已有同名应用"
	}
	return true, ""
}

// VolumeInfo returns the default volume and the volumes visible on this host.
func (s *Service) VolumeInfo(ctx context.Context) (string, []string) {
	def := s.cli.DefaultVolume(ctx)
	seen := map[string]bool{def: true}
	out := []string{def}
	entries, _ := os.ReadDir("/")
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "vol") || len(e.Name()) <= 3 {
			continue
		}
		n := strings.TrimPrefix(e.Name(), "vol")
		if _, err := parseDigits(n); err != nil {
			continue
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return def, out
}

func parseDigits(s string) (int, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
