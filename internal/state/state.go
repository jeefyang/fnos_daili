// Package state persists the manager's target inventory.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jeefyang/fnos_daili/internal/spec"
)

const Schema = 1

// Runtime states from the implementation spec.
const (
	StateDraft               = "draft"
	StateBuilding            = "building"
	StateInstalling          = "installing"
	StateStarting            = "starting"
	StateRunning             = "running"
	StateInstalledNotRunning = "installed_not_running"
	StateUninstalling        = "uninstalling"
	StateFailed              = "failed"
	StateBroken              = "broken"
	StateMissing             = "missing"
	StateUninstallFailed     = "uninstall_failed"
	StateUnmanaged           = "unmanaged"
)

// Operation statuses.
const (
	OpRunning     = "running"
	OpSucceeded   = "succeeded"
	OpFailed      = "failed"
	OpInterrupted = "interrupted"
)

// Operation is the last long-running action for a target.
type Operation struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	Step       string     `json:"step"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error"`
}

// Runtime is the observed and generated state of a target.
type Runtime struct {
	State             string     `json:"state"`
	NeedsReapply      bool       `json:"needs_reapply,omitempty"`
	InstalledVersion  string     `json:"installed_version,omitempty"`
	PackagePath       string     `json:"package_path,omitempty"`
	LastKnownGoodPath string     `json:"last_known_good_path,omitempty"`
	PackageSHA256     string     `json:"package_sha256,omitempty"`
	LastOperation     *Operation `json:"last_operation,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// Target couples the editable spec with its runtime state. Custom icons are
// stored alongside the spec so a generated package is reproducible after a
// manager restart; an empty pair means the built-in default icon.
type Target struct {
	Spec          spec.ProxySpec `json:"spec"`
	Runtime       Runtime        `json:"runtime"`
	CustomIcon64  []byte         `json:"custom_icon_64,omitempty"`
	CustomIcon256 []byte         `json:"custom_icon_256,omitempty"`
}

// Settings are manager-wide defaults for newly suggested appnames. They do
// not rename existing targets; appname stays immutable once created.
type Settings struct {
	AppNamePrefix string `json:"appname_prefix"`
	AppNameSuffix string `json:"appname_suffix"`
	// DNSServer is the default resolver for new targets. Empty means the
	// system resolver.
	DNSServer string `json:"dns_server,omitempty"`
}

// DefaultSettings returns the initial appname affixes. The prefix keeps the
// historical "proxy." look; the suffix is intentionally empty.
func DefaultSettings() *Settings {
	return &Settings{AppNamePrefix: "proxy."}
}

// State is the on-disk state.json document.
type State struct {
	Schema   int       `json:"schema"`
	Targets  []Target  `json:"targets"`
	Settings *Settings `json:"settings,omitempty"`
}

// New returns an empty state with the current schema and defaults.
func New() State { return State{Schema: Schema, Settings: DefaultSettings()} }

// Store reads and atomically writes state.json.
type Store struct {
	Path string
}

// Load reads state.json. A missing file is an empty state. A corrupt file is
// copied aside as state.json.bad.<unix> and an empty state is returned with a
// warning, so startup never blocks on damaged state.
func (s Store) Load() (State, string, error) {
	data, err := os.ReadFile(s.Path)
	if os.IsNotExist(err) {
		return New(), "", nil
	}
	if err != nil {
		return New(), "", err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		backup := s.Path + ".bad." + fmt.Sprint(time.Now().Unix())
		_ = os.WriteFile(backup, data, 0o600)
		return New(), "state.json is corrupt; backed up to " + backup + ": " + err.Error(), nil
	}
	if st.Schema != Schema {
		return New(), fmt.Sprintf("state.json schema %d is unsupported", st.Schema), nil
	}
	if st.Targets == nil {
		st.Targets = []Target{}
	}
	if st.Settings == nil {
		st.Settings = DefaultSettings()
	}
	return st, "", nil
}

// Save writes state atomically: temp file, fsync, chmod 0600, rename, fsync dir.
func (s Store) Save(st State) error {
	if st.Schema == 0 {
		st.Schema = Schema
	}
	if st.Targets == nil {
		st.Targets = []Target{}
	}
	if st.Settings == nil {
		st.Settings = DefaultSettings()
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		// Windows does not replace an existing file with Rename. The fallback
		// is only for tests on that platform; Linux keeps atomic rename.
		if removeErr := os.Remove(s.Path); removeErr != nil && !os.IsNotExist(removeErr) {
			return err
		}
		if err := os.Rename(tmpName, s.Path); err != nil {
			return err
		}
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
