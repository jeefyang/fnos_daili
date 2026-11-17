package manager

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jeefyang/fnos_daili/internal/state"
)

// ExportDocument is a self-contained backup of targets: the same field names
// the import endpoint accepts, plus the icon bytes when asked for.
type ExportDocument struct {
	Schema     int             `json:"schema"`
	ExportedAt time.Time       `json:"exported_at"`
	Settings   *state.Settings `json:"settings,omitempty"`
	Targets    []ExportTarget  `json:"targets"`
}

// ExportTarget mirrors SpecInput so a file exports and re-imports cleanly.
type ExportTarget struct {
	AppName       string      `json:"appname"`
	DisplayName   string      `json:"display_name"`
	Description   string      `json:"description,omitempty"`
	Version       string      `json:"version"`
	TargetURL     string      `json:"target_url"`
	Mode          string      `json:"mode"`
	EntryTitle    string      `json:"entry_title,omitempty"`
	UIType        string      `json:"ui_type,omitempty"`
	AllUsers      bool        `json:"all_users"`
	NoDisplay     bool        `json:"no_display,omitempty"`
	FileTypes     []string    `json:"file_types,omitempty"`
	InstallVolume string      `json:"install_volume,omitempty"`
	SkipTLSVerify bool        `json:"skip_tls_verify,omitempty"`
	AllowLoopback bool        `json:"allow_loopback,omitempty"`
	DNSServer     string      `json:"dns_server,omitempty"`
	BasicAuth     *ExportAuth `json:"basic_auth,omitempty"`
	Icon          string      `json:"icon,omitempty"`
	IconSource    string      `json:"icon_source,omitempty"`
	Port          int         `json:"port,omitempty"`
	State         string      `json:"state,omitempty"`
}

// ExportAuth carries the bcrypt hash so a migration keeps the same credentials.
// It is never returned by the normal target endpoints.
type ExportAuth struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

// Export builds a document for the given target ids (all targets when empty).
// It fails instead of silently dropping an id nobody knows.
func (s *Service) Export(ids []string, withIcons bool) (ExportDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	targets := s.st.Targets
	if len(ids) > 0 {
		chosen := make([]state.Target, 0, len(ids))
		for _, id := range ids {
			t := s.findLocked(id)
			if t == nil {
				return ExportDocument{}, fmt.Errorf("target %s: %w", id, errNotFound)
			}
			chosen = append(chosen, *t)
		}
		targets = chosen
	} else if len(targets) == 0 {
		return ExportDocument{}, errors.New("还没有可导出的目标")
	}
	doc := ExportDocument{Schema: 1, ExportedAt: s.now(), Targets: make([]ExportTarget, 0, len(targets))}
	if len(ids) == 0 {
		// A full backup carries the global settings too, so a restore rebuilds
		// the same naming convention. A single-target export leaves them out.
		st := *state.DefaultSettings()
		if s.st.Settings != nil {
			st = *s.st.Settings
		}
		doc.Settings = &st
	}
	for _, t := range targets {
		doc.Targets = append(doc.Targets, exportTargetOf(t, withIcons))
	}
	sort.SliceStable(doc.Targets, func(i, j int) bool { return doc.Targets[i].AppName < doc.Targets[j].AppName })
	return doc, nil
}

func exportTargetOf(t state.Target, withIcons bool) ExportTarget {
	out := ExportTarget{
		AppName:       t.Spec.AppName,
		DisplayName:   t.Spec.DisplayName,
		Description:   t.Spec.Description,
		Version:       t.Spec.Version,
		TargetURL:     t.Spec.TargetURL,
		Mode:          t.Spec.Mode,
		EntryTitle:    t.Spec.EntryTitle,
		UIType:        t.Spec.UIType,
		AllUsers:      t.Spec.AllUsers,
		NoDisplay:     t.Spec.NoDisplay,
		FileTypes:     append([]string(nil), t.Spec.FileTypes...),
		InstallVolume: t.Spec.InstallVolume,
		SkipTLSVerify: t.Spec.SkipTLSVerify,
		AllowLoopback: t.Spec.AllowLoopback,
		DNSServer:     t.Spec.DNSServer,
		Port:          t.Spec.Port,
		State:         t.Runtime.State,
	}
	if t.Spec.BasicAuth != nil {
		out.BasicAuth = &ExportAuth{Username: t.Spec.BasicAuth.Username, PasswordHash: t.Spec.BasicAuth.PasswordHash}
	}
	iconBytes := t.CustomIcon256
	if len(iconBytes) == 0 {
		iconBytes = t.CustomIcon64
	}
	if len(iconBytes) > 0 {
		out.IconSource = "custom"
		if withIcons {
			out.Icon = "data:image/png;base64," + base64.StdEncoding.EncodeToString(iconBytes)
		}
	} else {
		out.IconSource = "default"
	}
	return out
}

// exportFileName keeps the download name ASCII and readable.
func exportFileName(appname string) string {
	name := appname
	if name == "" {
		name = "targets"
	}
	return "daili-" + name + "-" + time.Now().Format("20060102-150405") + ".json"
}

func (s *Service) handleExport(w http.ResponseWriter, r *http.Request) {
	ids := splitIDList(r.URL.Query().Get("ids"))
	withIcons := r.URL.Query().Get("icons") != "0"
	doc, err := s.Export(ids, withIcons)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	name := "daili-targets-"
	if len(ids) == 1 {
		name = "daili-" + doc.Targets[0].AppName + "-"
	}
	writeDownload(w, name+time.Now().Format("20060102-150405")+".json", "application/json; charset=utf-8", doc)
}

func (s *Service) handleExportOne(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	withIcons := r.URL.Query().Get("icons") != "0"
	doc, err := s.Export([]string{id}, withIcons)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeDownload(w, exportFileName(doc.Targets[0].AppName), "application/json; charset=utf-8", doc)
}

func splitIDList(raw string) []string {
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
