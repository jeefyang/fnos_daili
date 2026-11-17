package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jeefyang/fnos_daili/internal/dnsx"
	"github.com/jeefyang/fnos_daili/internal/guard"
	"github.com/jeefyang/fnos_daili/internal/icon"
	"github.com/jeefyang/fnos_daili/internal/spec"
	"github.com/jeefyang/fnos_daili/internal/state"
	"github.com/jeefyang/fnos_daili/internal/target"
)

// Import row actions.
const (
	ImportCreate = "create"
	// ImportAdopt takes over an application that is installed on the host but
	// unknown to the manager: a restore after the manager was reinstalled
	// finds its proxies still running, so it uninstalls and re-installs them
	// from the backup instead of skipping them.
	ImportAdopt = "adopt"
	ImportSkip  = "skip"
)

// Batch item states, on top of the target runtime states.
const (
	BatchPending    = "pending"
	BatchProbing    = "probing"
	BatchInstalling = "installing"
	BatchSkipped    = "skipped"
)

// ImportRow is one line of the preview table.
type ImportRow struct {
	Index       int    `json:"index"`
	Action      string `json:"action"`
	AppName     string `json:"appname"`
	DisplayName string `json:"display_name"`
	TargetURL   string `json:"target_url"`
	Mode        string `json:"mode"`
	IconSource  string `json:"icon_source,omitempty"`
	Port        int    `json:"port,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// ImportPlan is what the preview shows before anything is written.
type ImportPlan struct {
	Rows       []ImportRow     `json:"rows"`
	Create     int             `json:"create"`
	Adopt      int             `json:"adopt"`
	Skip       int             `json:"skip"`
	ExportedAt string          `json:"exported_at,omitempty"`
	Settings   *state.Settings `json:"settings,omitempty"`
}

// importDocument is a parsed import file. Both hand-written files and exports
// fit: the difference is the optional settings block and exported_at stamp.
type importDocument struct {
	Targets    []SpecInput
	Settings   *state.Settings
	ExportedAt string
}

// BatchItemView is one line of the running batch table.
type BatchItemView struct {
	Index       int    `json:"index"`
	ID          string `json:"id,omitempty"`
	AppName     string `json:"appname"`
	DisplayName string `json:"display_name"`
	TargetURL   string `json:"target_url"`
	Port        int    `json:"port,omitempty"`
	Action      string `json:"action,omitempty"`
	State       string `json:"state"`
	OperationID string `json:"operation_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

// BatchView is the progress document the UI polls.
type BatchView struct {
	ID         string          `json:"id"`
	Status     string          `json:"status"`
	Install    bool            `json:"install"`
	Total      int             `json:"total"`
	Done       int             `json:"done"`
	Failed     int             `json:"failed"`
	Items      []BatchItemView `json:"items"`
	Skipped    []ImportRow     `json:"skipped"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

type batchRun struct {
	view  BatchView
	ids   []string
	kinds []string
}

// parseImportDocument accepts either a bare array of targets or an object with
// a "targets" array, so both hand-written and exported files work.
func parseImportDocument(data []byte) (importDocument, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return importDocument{}, errors.New("文件是空的")
	}
	if strings.HasPrefix(trimmed, "[") {
		var items []SpecInput
		if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
			return importDocument{}, fmt.Errorf("JSON 解析失败：%w", err)
		}
		return importDocument{Targets: items}, nil
	}
	var raw struct {
		Targets    []SpecInput     `json:"targets"`
		Settings   *state.Settings `json:"settings"`
		ExportedAt string          `json:"exported_at"`
	}
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return importDocument{}, fmt.Errorf("JSON 解析失败：%w", err)
	}
	if raw.Targets == nil {
		return importDocument{}, errors.New("找不到 targets 数组")
	}
	return importDocument{Targets: raw.Targets, Settings: raw.Settings, ExportedAt: raw.ExportedAt}, nil
}

// PlanImport validates every row and decides create/adopt/skip without writing
// anything. The CLI is asked once for the installed application list; a name
// that is installed but not managed by this manager is a restore candidate.
func (s *Service) PlanImport(ctx context.Context, doc importDocument, defaultDNS string) (ImportPlan, []spec.ProxySpec, []icon.Set) {
	in := doc.Targets
	plan := ImportPlan{ExportedAt: doc.ExportedAt}
	if doc.Settings != nil {
		st := *doc.Settings
		plan.Settings = &st
	}
	specs := make([]spec.ProxySpec, len(in))
	icons := make([]icon.Set, len(in))

	managed := map[string]bool{}
	s.mu.Lock()
	for _, t := range s.st.Targets {
		managed[t.Spec.AppName] = true
	}
	s.mu.Unlock()
	installed := map[string]bool{}
	if names, err := s.cliNames(ctx); err == nil {
		for name := range names {
			if !managed[name] {
				installed[name] = true
			}
		}
	} else {
		s.logger.Printf("import: cannot list applications: %v", err)
	}
	seen := map[string]bool{}
	for name := range managed {
		seen[name] = true
	}
	for name := range installed {
		seen[name] = true
	}

	used := map[string]bool{}
	for i, item := range in {
		row := ImportRow{Index: i + 1, Action: ImportCreate}
		item.IconData = ""
		item.RemoveIcon = false
		if strings.TrimSpace(item.AppName) == "" {
			item.AppName = s.suggestImportAppName(item, used, seen)
		}
		if item.Port != 0 && (item.Port < spec.PortMin || item.Port > spec.PortMax) {
			row.Action = ImportSkip
			row.AppName = strings.TrimSpace(item.AppName)
			row.DisplayName = strings.TrimSpace(item.DisplayName)
			row.TargetURL = strings.TrimSpace(item.TargetURL)
			row.Reason = fmt.Sprintf("端口 %d 不在 %d-%d 范围内", item.Port, spec.PortMin, spec.PortMax)
			plan.Rows = append(plan.Rows, row)
			plan.Skip++
			continue
		}
		sp, err := s.specFromInput(item, nil, defaultDNS)
		if err != nil {
			row.Action = ImportSkip
			row.AppName = strings.TrimSpace(item.AppName)
			row.DisplayName = strings.TrimSpace(item.DisplayName)
			row.TargetURL = strings.TrimSpace(item.TargetURL)
			row.Reason = "字段不合法：" + err.Error()
			plan.Rows = append(plan.Rows, row)
			plan.Skip++
			continue
		}
		set, _, err := iconFromInput(item)
		if err != nil {
			row.Action = ImportSkip
			row.AppName = sp.AppName
			row.DisplayName = sp.DisplayName
			row.TargetURL = sp.TargetURL
			row.Mode = sp.Mode
			row.Reason = "图标不合法：" + err.Error()
			plan.Rows = append(plan.Rows, row)
			plan.Skip++
			continue
		}
		sp.Port = item.Port
		row.AppName = sp.AppName
		row.DisplayName = sp.DisplayName
		row.TargetURL = sp.TargetURL
		row.Mode = sp.Mode
		row.Port = sp.Port
		row.IconSource = "default"
		if len(set.Icon64) > 0 {
			row.IconSource = "custom"
		}
		switch {
		case used[sp.AppName]:
			row.Action = ImportSkip
			row.Reason = "文件里重复的 appname"
		case managed[sp.AppName]:
			row.Action = ImportSkip
			row.Reason = "托管台已经在管这个 appname；要改它请直接编辑"
		case installed[sp.AppName]:
			row.Action = ImportAdopt
			row.Reason = "系统里已有同名应用但不归托管台管；导入后卸载并重装为这份配置"
		}
		used[sp.AppName] = true
		switch row.Action {
		case ImportSkip:
			plan.Skip++
		case ImportAdopt:
			plan.Adopt++
			specs[i] = sp
			icons[i] = set
		default:
			plan.Create++
			specs[i] = sp
			icons[i] = set
		}
		plan.Rows = append(plan.Rows, row)
	}
	return plan, specs, icons
}

// suggestImportAppName derives an appname for rows that leave it out. The
// result respects the global prefix/suffix and never collides with an entry
// already used in this file or installed on the host.
func (s *Service) suggestImportAppName(item SpecInput, used, seen map[string]bool) string {
	st := s.Settings()
	prefix, suffix := st.AppNamePrefix, st.AppNameSuffix
	base := slugForAppName(item.DisplayName)
	if base == "" {
		base = slugForAppName(hostOfTarget(item.TargetURL))
	}
	if base == "" {
		base = "target"
	}
	for attempt := 0; attempt < 64; attempt++ {
		name := prefix + base
		if attempt > 1 {
			name += strconv.Itoa(attempt)
		}
		name += suffix
		if spec.ValidateAppName(name) != nil {
			name = prefix + "target" + strconv.Itoa(attempt) + suffix
			if spec.ValidateAppName(name) != nil {
				continue
			}
		}
		if !used[name] && !seen[name] {
			return name
		}
	}
	return ""
}

// slugForAppName keeps ASCII letters and digits, folding everything else into
// single dashes, so the result satisfies the appname character set.
func slugForAppName(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '.' || r == '_' || r == '-' || r == ':' || r == '/':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	for len(out) > 24 {
		out = strings.TrimSuffix(out, "-")
		if len(out) <= 24 {
			break
		}
		out = out[:24]
	}
	return strings.Trim(out, "-")
}

func hostOfTarget(raw string) string {
	norm, err := target.Normalize(raw)
	if err != nil {
		return ""
	}
	u, err := url.Parse(norm)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// ImportBulk creates drafts for every row that passed validation and, when
// install is true, starts a background batch that installs them one at a time
// under the global CLI lock.
func (s *Service) ImportBulk(ctx context.Context, doc importDocument, install, skipProbe, restoreSettings bool) (BatchView, error) {
	if restoreSettings && doc.Settings != nil {
		if _, err := s.SaveSettings(*doc.Settings); err != nil {
			return BatchView{}, err
		}
	}
	plan, specs, icons := s.PlanImport(ctx, doc, s.Settings().DNSServer)
	var created []BatchItemView
	var kinds []string
	var skipped []ImportRow
	for i, row := range plan.Rows {
		if row.Action == ImportSkip {
			skipped = append(skipped, row)
			continue
		}
		view, err := s.createFromSpec(specs[i], icons[i], specs[i].Port)
		if err != nil {
			row.Action = ImportSkip
			row.Reason = err.Error()
			skipped = append(skipped, row)
			continue
		}
		kind := "install"
		if row.Action == ImportAdopt {
			kind = "reinstall"
		}
		kinds = append(kinds, kind)
		created = append(created, BatchItemView{
			Index:       row.Index,
			ID:          view.Spec.ID,
			AppName:     view.Spec.AppName,
			DisplayName: view.Spec.DisplayName,
			TargetURL:   view.Spec.TargetURL,
			Port:        view.Spec.Port,
			Action:      row.Action,
			State:       BatchPending,
		})
	}
	if len(created) == 0 {
		return BatchView{Status: "done", Total: 0, Skipped: skipped}, nil
	}
	b := &batchRun{kinds: kinds, view: BatchView{
		ID:        newID(),
		Status:    "running",
		Install:   install,
		Total:     len(created),
		Items:     created,
		Skipped:   skipped,
		StartedAt: s.now(),
	}}
	for _, it := range created {
		b.ids = append(b.ids, it.ID)
	}
	if !install {
		now := s.now()
		b.view.Status = "done"
		b.view.FinishedAt = &now
	}
	s.mu.Lock()
	if s.batches == nil {
		s.batches = map[string]*batchRun{}
	}
	s.batches[b.view.ID] = b
	view := b.viewCopy()
	s.mu.Unlock()
	if install {
		go s.runBatch(b, skipProbe)
	}
	return view, nil
}

func (s *Service) createFromSpec(sp spec.ProxySpec, set icon.Set, preferPort int) (TargetView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byAppLocked(sp.AppName) != nil {
		return TargetView{}, &spec.Error{Field: "appname", Message: "already exists"}
	}
	port, err := s.allocatePortPreferLocked(preferPort)
	if err != nil {
		return TargetView{}, err
	}
	sp.ID = newID()
	sp.Port = port
	now := s.now()
	t := state.Target{Spec: sp, Runtime: state.Runtime{State: state.StateDraft, CreatedAt: now, UpdatedAt: now}}
	t.CustomIcon64 = set.Icon64
	t.CustomIcon256 = set.Icon256
	s.st.Targets = append(s.st.Targets, t)
	if err := s.saveLocked(); err != nil {
		return TargetView{}, err
	}
	return viewOf(t), nil
}

// runBatch installs the created targets one after another. It holds the global
// CLI lock for the whole batch, so a single import cannot interleave with a
// manual install, and it never aborts the remainder when one target fails.
func (s *Service) runBatch(b *batchRun, skipProbe bool) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	for i := range b.view.Items {
		s.updateBatchItem(b, i, func(it *BatchItemView) {
			it.State = BatchProbing
		})
		if !skipProbe {
			if reason := s.probeBatchTarget(b.ids[i]); reason != "" {
				s.updateBatchItem(b, i, func(it *BatchItemView) {
					it.State = state.StateDraft
					it.Error = reason
				})
				continue
			}
		}
		kind := "install"
		if i < len(b.kinds) && b.kinds[i] != "" {
			kind = b.kinds[i]
		}
		opID, err := s.beginBatchOperation(b.ids[i], kind)
		if err != nil {
			s.updateBatchItem(b, i, func(it *BatchItemView) {
				it.State = state.StateDraft
				it.Error = err.Error()
			})
			continue
		}
		s.updateBatchItem(b, i, func(it *BatchItemView) {
			it.State = BatchInstalling
			it.OperationID = opID
		})
		err = s.executeOperation(b.ids[i], kind, opID)
		s.updateBatchItem(b, i, func(it *BatchItemView) {
			if err != nil {
				it.State = state.StateFailed
				it.Error = err.Error()
				return
			}
			it.State = state.StateRunning
		})
	}
	s.mu.Lock()
	if cur, ok := s.batches[b.view.ID]; ok {
		now := s.now()
		cur.view.Status = "done"
		cur.view.FinishedAt = &now
	}
	s.mu.Unlock()
}

func (s *Service) beginBatchOperation(id, kind string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.findLocked(id)
	if t == nil {
		return "", errNotFound
	}
	return s.beginOperationLocked(t, kind)
}

// probeBatchTarget checks that the target accepts a TCP connection before the
// package is installed. A draft that never reaches its target stays a draft so
// the operator can fix the address and retry.
func (s *Service) probeBatchTarget(id string) string {
	t, ok := s.getTarget(id)
	if !ok {
		return "目标已不存在"
	}
	if t.Spec.Mode == "direct" {
		return ""
	}
	u, err := url.Parse(t.Spec.TargetURL)
	if err != nil {
		return "目标地址不合法"
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	g := guard.Guard{AllowLoopback: t.Spec.AllowLoopback}
	if t.Spec.DNSServer != "" {
		g.Resolver = dnsx.New(t.Spec.DNSServer)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := g.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), strconv.Itoa(port)))
	if err != nil {
		code, detail := targetCheckError(err)
		if detail != "" {
			return "目标不可达（" + code + "）：" + detail
		}
		return "目标不可达（" + code + "）"
	}
	_ = conn.Close()
	return ""
}

func (s *Service) updateBatchItem(b *batchRun, i int, fn func(*BatchItemView)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.batches[b.view.ID]
	if !ok || i >= len(cur.view.Items) {
		return
	}
	fn(&cur.view.Items[i])
	done, failed := 0, 0
	for _, it := range cur.view.Items {
		switch it.State {
		case BatchPending, BatchProbing, BatchInstalling:
		default:
			done++
			if it.State != state.StateRunning {
				failed++
			}
		}
	}
	cur.view.Done = done
	cur.view.Failed = failed
}

// Batch returns a snapshot of one batch.
func (s *Service) Batch(id string) (BatchView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.batches[id]
	if !ok {
		return BatchView{}, false
	}
	return b.viewCopy(), true
}

// Batches returns recent batches, newest first.
func (s *Service) Batches() []BatchView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]BatchView, 0, len(s.batches))
	for _, b := range s.batches {
		out = append(out, b.viewCopy())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

func (b *batchRun) viewCopy() BatchView {
	v := b.view
	v.Items = append([]BatchItemView(nil), b.view.Items...)
	v.Skipped = append([]ImportRow(nil), b.view.Skipped...)
	return v
}

// cliNames returns the installed application names reported by the CLI.
func (s *Service) cliNames(ctx context.Context) (map[string]bool, error) {
	apps, err := s.cli.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(apps))
	for _, a := range apps {
		out[a.AppName] = true
	}
	return out, nil
}
