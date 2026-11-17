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
	"github.com/jeefyang/fnos_daili/internal/spec"
	"github.com/jeefyang/fnos_daili/internal/state"
	"github.com/jeefyang/fnos_daili/internal/target"
)

// Import row actions.
const (
	ImportCreate = "create"
	ImportSkip   = "skip"
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
	Reason      string `json:"reason,omitempty"`
}

// ImportPlan is what the preview shows before anything is written.
type ImportPlan struct {
	Rows   []ImportRow `json:"rows"`
	Create int         `json:"create"`
	Skip   int         `json:"skip"`
}

// BatchItemView is one line of the running batch table.
type BatchItemView struct {
	Index       int    `json:"index"`
	ID          string `json:"id,omitempty"`
	AppName     string `json:"appname"`
	DisplayName string `json:"display_name"`
	TargetURL   string `json:"target_url"`
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
	view BatchView
	ids  []string
}

// parseImportDocument accepts either a bare array of targets or an object with
// a "targets" array, so both hand-written and exported files work.
func parseImportDocument(data []byte) ([]SpecInput, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, errors.New("文件是空的")
	}
	if strings.HasPrefix(trimmed, "[") {
		var items []SpecInput
		if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
			return nil, fmt.Errorf("JSON 解析失败：%w", err)
		}
		return items, nil
	}
	var doc struct {
		Targets []SpecInput `json:"targets"`
	}
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		return nil, fmt.Errorf("JSON 解析失败：%w", err)
	}
	if doc.Targets == nil {
		return nil, errors.New("找不到 targets 数组")
	}
	return doc.Targets, nil
}

// PlanImport validates every row and decides create/skip without writing
// anything. The CLI is asked once for the installed application list.
func (s *Service) PlanImport(ctx context.Context, in []SpecInput, defaultDNS string) (ImportPlan, []spec.ProxySpec) {
	plan := ImportPlan{}
	specs := make([]spec.ProxySpec, len(in))
	seen := map[string]bool{}
	if installed, err := s.cliNames(ctx); err == nil {
		for name := range installed {
			seen[name] = true
		}
	} else {
		s.logger.Printf("import: cannot list applications: %v", err)
	}
	s.mu.Lock()
	for _, t := range s.st.Targets {
		seen[t.Spec.AppName] = true
	}
	s.mu.Unlock()

	used := map[string]bool{}
	for i, item := range in {
		row := ImportRow{Index: i + 1, Action: ImportCreate}
		item.IconData = ""
		item.RemoveIcon = false
		if strings.TrimSpace(item.AppName) == "" {
			item.AppName = s.suggestImportAppName(item, used, seen)
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
		row.AppName = sp.AppName
		row.DisplayName = sp.DisplayName
		row.TargetURL = sp.TargetURL
		row.Mode = sp.Mode
		switch {
		case used[sp.AppName]:
			row.Action = ImportSkip
			row.Reason = "文件里重复的 appname"
		case seen[sp.AppName]:
			row.Action = ImportSkip
			row.Reason = "appname 已被占用（托管台或系统里已有同名应用）"
		}
		used[sp.AppName] = true
		if row.Action == ImportSkip {
			plan.Skip++
		} else {
			plan.Create++
			specs[i] = sp
		}
		plan.Rows = append(plan.Rows, row)
	}
	return plan, specs
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
func (s *Service) ImportBulk(ctx context.Context, in []SpecInput, install, skipProbe bool) (BatchView, error) {
	plan, specs := s.PlanImport(ctx, in, s.Settings().DNSServer)
	var created []BatchItemView
	var skipped []ImportRow
	for i, row := range plan.Rows {
		if row.Action == ImportSkip {
			skipped = append(skipped, row)
			continue
		}
		view, err := s.createFromSpec(specs[i])
		if err != nil {
			row.Action = ImportSkip
			row.Reason = err.Error()
			skipped = append(skipped, row)
			continue
		}
		created = append(created, BatchItemView{
			Index:       row.Index,
			ID:          view.Spec.ID,
			AppName:     view.Spec.AppName,
			DisplayName: view.Spec.DisplayName,
			TargetURL:   view.Spec.TargetURL,
			State:       BatchPending,
		})
	}
	if len(created) == 0 {
		return BatchView{Status: "done", Total: 0, Skipped: skipped}, nil
	}
	b := &batchRun{view: BatchView{
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

func (s *Service) createFromSpec(sp spec.ProxySpec) (TargetView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byAppLocked(sp.AppName) != nil {
		return TargetView{}, &spec.Error{Field: "appname", Message: "already exists"}
	}
	port, err := s.allocatePortLocked()
	if err != nil {
		return TargetView{}, err
	}
	sp.ID = newID()
	sp.Port = port
	now := s.now()
	t := state.Target{Spec: sp, Runtime: state.Runtime{State: state.StateDraft, CreatedAt: now, UpdatedAt: now}}
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
		opID, err := s.beginBatchOperation(b.ids[i])
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
		err = s.executeOperation(b.ids[i], "install", opID)
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

func (s *Service) beginBatchOperation(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.findLocked(id)
	if t == nil {
		return "", errNotFound
	}
	return s.beginOperationLocked(t, "install")
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
