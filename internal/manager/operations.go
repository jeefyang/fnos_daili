package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jeefyang/fnos_daili/internal/cli"
	"github.com/jeefyang/fnos_daili/internal/fpk"
	"github.com/jeefyang/fnos_daili/internal/guard"
	"github.com/jeefyang/fnos_daili/internal/spec"
	"github.com/jeefyang/fnos_daili/internal/state"
)

const (
	cliInstallTimeout   = 60 * time.Second
	cliUninstallTimeout = 60 * time.Second
	cliStartTimeout     = 30 * time.Second
	cliQueryTimeout     = 10 * time.Second
	waitRunningTimeout  = 300 * time.Second
	waitStartTimeout    = 60 * time.Second
	operationTimeout    = 15 * time.Minute
)

// Apply starts an install/upgrade/reinstall and returns its operation id.
func (s *Service) Apply(id, kind string) (string, error) {
	s.mu.Lock()
	t := s.findLocked(id)
	if t == nil {
		s.mu.Unlock()
		return "", errNotFound
	}
	if kind == "" {
		kind = inferKind(t.Runtime.State)
	}
	switch kind {
	case "install", "upgrade", "reinstall":
	default:
		s.mu.Unlock()
		return "", &specError{Field: "kind", Message: "must be install, upgrade or reinstall"}
	}
	if s.running[id] || (t.Runtime.LastOperation != nil && t.Runtime.LastOperation.Status == state.OpRunning) {
		s.mu.Unlock()
		return "", errConflict
	}
	opID, err := s.beginOperationLocked(t, kind)
	s.mu.Unlock()
	if err != nil {
		return "", err
	}

	go func() {
		s.opMu.Lock()
		defer s.opMu.Unlock()
		s.executeOperation(id, kind, opID)
	}()
	return opID, nil
}

// beginOperationLocked marks a target as busy and records the operation. The
// caller must hold s.mu; the state file is written before the work starts so a
// restart can tell an interrupted operation from an idle target.
func (s *Service) beginOperationLocked(t *state.Target, kind string) (string, error) {
	opID := newID()
	now := s.now()
	t.Runtime.LastOperation = &state.Operation{ID: opID, Kind: kind, Status: state.OpRunning, Step: "validate", StartedAt: now}
	t.Runtime.State = state.StateBuilding
	t.Runtime.LastError = ""
	t.Runtime.UpdatedAt = now
	s.running[t.Spec.ID] = true
	s.ops[opID] = t.Spec.ID
	if err := s.saveLocked(); err != nil {
		delete(s.running, t.Spec.ID)
		delete(s.ops, opID)
		return "", err
	}
	return opID, nil
}

// executeOperation runs one install/upgrade/reinstall to completion and
// records the outcome. Callers hold the global CLI lock.
func (s *Service) executeOperation(id, kind, opID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()
	err := s.runApply(ctx, id, kind, opID)
	s.finishOperation(id, opID, err)
	return err
}

func inferKind(st string) string {
	switch st {
	case state.StateRunning, state.StateInstalledNotRunning, state.StateMissing:
		return "upgrade"
	case state.StateFailed, state.StateBroken, state.StateUninstallFailed:
		return "reinstall"
	default:
		return "install"
	}
}

func (s *Service) runApply(ctx context.Context, id, kind, opID string) error {
	t, ok := s.getTarget(id)
	if !ok {
		return errNotFound
	}
	if t.Spec.Mode == "reverse" {
		if err := checkTargetGuard(ctx, t.Spec); err != nil {
			var re *guard.ResolveError
			if !errors.As(err, &re) {
				return err
			}
			s.logger.Printf("target %s: DNS check deferred: %v", t.Spec.AppName, err)
		}
	}

	s.setStep(id, opID, "build", state.StateBuilding)
	pkgPath, sha, err := s.buildPackage(t, opID)
	if err != nil {
		return err
	}
	s.setStep(id, opID, "install", state.StateInstalling)

	app, err := s.findApp(ctx, t.Spec.AppName)
	if err != nil {
		return err
	}
	if app != nil {
		if kind == "install" && !isInstalledState(t.Runtime.State) {
			return &cli.Error{Code: "appname_conflict", Message: "系统里已有同名应用，且不属于本托管台"}
		}
		if err := s.removeForReplace(ctx, t.Spec.AppName, app.Status); err != nil {
			return err
		}
	}

	installCtx, cancel := context.WithTimeout(ctx, cliInstallTimeout)
	_, err = s.cli.Install(installCtx, t.Spec.InstallVolume, pkgPath)
	cancel()
	if err != nil {
		return s.afterApplyFailure(ctx, id, kind, t, err)
	}

	s.setStep(id, opID, "start", state.StateStarting)
	if err := s.waitStarted(ctx, t.Spec.AppName); err != nil {
		return s.afterApplyFailure(ctx, id, kind, t, err)
	}

	s.mu.Lock()
	if cur := s.findLocked(id); cur != nil {
		cur.Runtime.State = state.StateRunning
		cur.Runtime.InstalledVersion = t.Spec.Version
		cur.Runtime.PackagePath = pkgPath
		cur.Runtime.PackageSHA256 = sha
		cur.Runtime.LastKnownGoodPath = pkgPath
		cur.Runtime.NeedsReapply = false
		cur.Runtime.LastError = ""
		cur.Runtime.UpdatedAt = s.now()
	}
	err = s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.cleanupPackages(t.Spec.AppName, pkgPath)
	return nil
}

func (s *Service) afterApplyFailure(ctx context.Context, id, kind string, t state.Target, cause error) error {
	if kind != "upgrade" || t.Runtime.LastKnownGoodPath == "" {
		return cause
	}
	old := t.Runtime.LastKnownGoodPath
	if _, err := os.Stat(old); err != nil {
		return cause
	}
	if err := s.removeForReplace(ctx, t.Spec.AppName, "running"); err != nil {
		_ = s.setState(id, state.StateBroken, "回滚失败："+err.Error())
		return fmt.Errorf("%w；回滚失败：%v", cause, err)
	}
	installCtx, cancel := context.WithTimeout(ctx, cliInstallTimeout)
	_, err := s.cli.Install(installCtx, t.Spec.InstallVolume, old)
	cancel()
	if err != nil {
		_ = s.setState(id, state.StateBroken, "回滚失败："+err.Error())
		return fmt.Errorf("%w；回滚失败：%v", cause, err)
	}
	if err := s.waitStarted(ctx, t.Spec.AppName); err != nil {
		_ = s.setState(id, state.StateBroken, "回滚后未启动："+err.Error())
		return fmt.Errorf("%w；回滚后未启动：%v", cause, err)
	}
	_ = s.setState(id, state.StateFailed, "升级失败，已回滚到上一版："+cause.Error())
	return cause
}

func (s *Service) waitStarted(ctx context.Context, appname string) error {
	waitCtx, cancel := context.WithTimeout(ctx, waitRunningTimeout)
	st, err := s.cli.WaitRunning(waitCtx, appname)
	cancel()
	if err != nil {
		_ = s.setStateByApp(appname, state.StateInstalledNotRunning, "等待启动超时")
		return &cli.Error{Code: "not_running", Message: "安装完成但应用未在 5 分钟内进入 running"}
	}
	if st == "running" {
		return nil
	}
	if st == "stopped" {
		startCtx, cancel := context.WithTimeout(ctx, cliStartTimeout)
		err := s.cli.Start(startCtx, appname)
		cancel()
		if err != nil {
			_ = s.setStateByApp(appname, state.StateInstalledNotRunning, err.Error())
			return err
		}
		waitCtx, cancel = context.WithTimeout(ctx, waitStartTimeout)
		st, err = s.cli.WaitRunning(waitCtx, appname)
		cancel()
		if err == nil && st == "running" {
			return nil
		}
		_ = s.setStateByApp(appname, state.StateInstalledNotRunning, "启动后仍未进入 running")
		return &cli.Error{Code: "not_running", Message: "应用已安装，但启动后仍未进入 running"}
	}
	_ = s.setStateByApp(appname, state.StateInstalledNotRunning, "应用状态为 "+st)
	return &cli.Error{Code: "not_running", Message: "应用未进入 running，当前状态：" + st}
}

func (s *Service) removeForReplace(ctx context.Context, appname, status string) error {
	status = strings.ToLower(status)
	if status == "starting" || status == "stopping" {
		waitCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		st, err := s.cli.WaitRunning(waitCtx, appname)
		cancel()
		if err != nil {
			return &cli.Error{Code: "not_running", Message: "应用仍在 " + status + "，无法卸载"}
		}
		status = st
	}
	unCtx, cancel := context.WithTimeout(ctx, cliUninstallTimeout)
	_, err := s.cli.Uninstall(unCtx, appname)
	cancel()
	if err != nil {
		if is10500(err) {
			select {
			case <-ctx.Done():
				return err
			case <-time.After(5 * time.Second):
			}
			unCtx, cancel = context.WithTimeout(ctx, cliUninstallTimeout)
			_, err = s.cli.Uninstall(unCtx, appname)
			cancel()
		}
		if err != nil {
			return err
		}
	}
	goneCtx, cancel := context.WithTimeout(ctx, cliUninstallTimeout)
	defer cancel()
	if err := s.cli.WaitGone(goneCtx, appname); err != nil {
		return &cli.Error{Code: "uninstall_timeout", Message: "卸载后应用仍未从列表消失"}
	}
	return nil
}

func is10500(err error) bool {
	var ce *cli.Error
	if errors.As(err, &ce) {
		return strings.Contains(ce.Output, "10500")
	}
	return false
}

func (s *Service) findApp(ctx context.Context, appname string) (*cli.App, error) {
	qCtx, cancel := context.WithTimeout(ctx, cliQueryTimeout)
	defer cancel()
	return s.cli.Find(qCtx, appname)
}

func (s *Service) buildPackage(t state.Target, opID string) (string, string, error) {
	if len(s.cfg.ProxyBinary) == 0 {
		return "", "", &cli.Error{Code: "missing_proxy_binary", Message: "托管台内没有代理二进制，无法生成安装包"}
	}
	icon64, icon256 := s.cfg.Icon64, s.cfg.Icon256
	if len(t.CustomIcon64) > 0 {
		icon64 = t.CustomIcon64
	}
	if len(t.CustomIcon256) > 0 {
		icon256 = t.CustomIcon256
	}
	var buf strings.Builder
	_, err := fpk.BuildProxy(&buf, fpk.BuildOptions{
		Spec: t.Spec,
		Assets: fpk.Assets{
			ProxyBinary: s.cfg.ProxyBinary,
			Icon64:      icon64,
			Icon256:     icon256,
		},
		Platform: s.cfg.Platform,
	})
	if err != nil {
		return "", "", err
	}
	data := []byte(buf.String())
	if _, err := fpk.Verify(data); err != nil {
		return "", "", fmt.Errorf("生成的包未通过自检: %w", err)
	}
	dir := filepath.Join(s.cfg.DataDir, "packages", t.Spec.AppName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	name := fmt.Sprintf("%s-%d-%s.fpk", t.Spec.Version, s.now().Unix(), opID[:8])
	path := filepath.Join(dir, name)
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(data)
	return path, hex.EncodeToString(sum[:]), nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".pkg-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
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
	if err := os.Rename(name, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return err
		}
		return os.Rename(name, path)
	}
	return nil
}

func (s *Service) cleanupPackages(appname, keep string) {
	dir := filepath.Join(s.cfg.DataDir, "packages", appname)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		path string
		mod  time.Time
	}
	var files []item
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".fpk") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, item{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	keepSet := map[string]bool{keep: true}
	if s.mu.Lock(); true {
		if t := s.byAppLocked(appname); t != nil {
			if t.Runtime.LastKnownGoodPath != "" {
				keepSet[t.Runtime.LastKnownGoodPath] = true
			}
		}
		s.mu.Unlock()
	}
	kept := 0
	for _, f := range files {
		if keepSet[f.path] {
			continue
		}
		if kept < 1 {
			kept++
			continue
		}
		_ = os.Remove(f.path)
	}
}

func (s *Service) getTarget(id string) (state.Target, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.findLocked(id)
	if t == nil {
		return state.Target{}, false
	}
	return *t, true
}

func (s *Service) setStep(id, opID, step, targetState string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.findLocked(id)
	if t == nil || t.Runtime.LastOperation == nil || t.Runtime.LastOperation.ID != opID {
		return
	}
	t.Runtime.LastOperation.Step = step
	t.Runtime.State = targetState
	t.Runtime.UpdatedAt = s.now()
	_ = s.saveLocked()
}

func (s *Service) setState(id, targetState, lastErr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.findLocked(id)
	if t == nil {
		return errNotFound
	}
	t.Runtime.State = targetState
	t.Runtime.LastError = lastErr
	t.Runtime.UpdatedAt = s.now()
	return s.saveLocked()
}

func (s *Service) setStateByApp(appname, targetState, lastErr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.byAppLocked(appname)
	if t == nil {
		return errNotFound
	}
	t.Runtime.State = targetState
	t.Runtime.LastError = lastErr
	t.Runtime.UpdatedAt = s.now()
	return s.saveLocked()
}

func (s *Service) finishOperation(id, opID string, opErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.findLocked(id); t != nil && t.Runtime.LastOperation != nil && t.Runtime.LastOperation.ID == opID {
		now := s.now()
		t.Runtime.LastOperation.FinishedAt = &now
		t.Runtime.LastOperation.Step = "done"
		if opErr != nil {
			t.Runtime.LastOperation.Status = state.OpFailed
			t.Runtime.LastOperation.Error = opErr.Error()
			if t.Runtime.State != state.StateBroken && t.Runtime.State != state.StateFailed {
				t.Runtime.State = state.StateFailed
			}
			t.Runtime.LastError = opErr.Error()
		} else {
			t.Runtime.LastOperation.Status = state.OpSucceeded
			t.Runtime.LastOperation.Error = ""
			t.Runtime.LastError = ""
		}
		t.Runtime.UpdatedAt = now
		if err := s.saveLocked(); err != nil {
			s.logger.Printf("save state after operation: %v", err)
		}
		s.recordDoneLocked(opID, *t.Runtime.LastOperation)
	}
	delete(s.running, id)
	delete(s.ops, opID)
}

// recordDoneLocked keeps a bounded history so GET /api/operations/{id} still
// answers after a target (and its state entry) has been deleted.
func (s *Service) recordDoneLocked(id string, op state.Operation) {
	if s.done == nil {
		s.done = map[string]state.Operation{}
	}
	s.done[id] = op
	for len(s.done) > 256 {
		for k := range s.done {
			if k != id {
				delete(s.done, k)
				break
			}
		}
	}
}

// Delete starts an asynchronous uninstall and state removal.
func (s *Service) Delete(id string) (string, error) {
	s.mu.Lock()
	t := s.findLocked(id)
	if t == nil {
		s.mu.Unlock()
		return "", errNotFound
	}
	if s.running[id] || (t.Runtime.LastOperation != nil && t.Runtime.LastOperation.Status == state.OpRunning) {
		s.mu.Unlock()
		return "", errConflict
	}
	opID := newID()
	now := s.now()
	t.Runtime.LastOperation = &state.Operation{ID: opID, Kind: "uninstall", Status: state.OpRunning, Step: "uninstall", StartedAt: now}
	t.Runtime.State = state.StateUninstalling
	t.Runtime.LastError = ""
	t.Runtime.UpdatedAt = now
	s.running[id] = true
	s.ops[opID] = id
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		delete(s.running, id)
		delete(s.ops, opID)
		return "", err
	}
	appname := t.Spec.AppName
	s.mu.Unlock()

	go func() {
		s.opMu.Lock()
		defer s.opMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
		defer cancel()
		app, err := s.findApp(ctx, appname)
		if err != nil {
			_ = s.setState(id, state.StateUninstallFailed, err.Error())
			s.finishOperation(id, opID, err)
			return
		}
		if app == nil {
			if err := s.removeTarget(id, appname, opID); err != nil {
				s.logger.Printf("remove target %s: %v", appname, err)
			}
			return
		}
		unCtx, c2 := context.WithTimeout(ctx, cliUninstallTimeout)
		_, err = s.cli.Uninstall(unCtx, appname)
		c2()
		if err != nil {
			if is10500(err) {
				select {
				case <-ctx.Done():
				case <-time.After(5 * time.Second):
				}
				unCtx, c2 = context.WithTimeout(ctx, cliUninstallTimeout)
				_, err = s.cli.Uninstall(unCtx, appname)
				c2()
			}
		}
		if err == nil {
			goneCtx, c3 := context.WithTimeout(ctx, cliUninstallTimeout)
			err = s.cli.WaitGone(goneCtx, appname)
			c3()
		}
		if err != nil {
			_ = s.setState(id, state.StateUninstallFailed, err.Error())
			s.finishOperation(id, opID, err)
			return
		}
		if err := s.removeTarget(id, appname, opID); err != nil {
			s.logger.Printf("remove target %s: %v", appname, err)
		}
	}()
	return opID, nil
}

// removeTarget finalizes a successful uninstall: it records the terminal
// operation, drops the target from state, releases the operation slots, and
// deletes the target's cached packages.
func (s *Service) removeTarget(id, appname, opID string) error {
	s.mu.Lock()
	if t := s.findLocked(id); t != nil {
		if op := t.Runtime.LastOperation; op != nil && op.ID == opID {
			now := s.now()
			op.Status = state.OpSucceeded
			op.Step = "done"
			op.FinishedAt = &now
			op.Error = ""
			s.recordDoneLocked(opID, *op)
		}
		idx := -1
		for i := range s.st.Targets {
			if s.st.Targets[i].Spec.ID == id {
				idx = i
				break
			}
		}
		if idx >= 0 {
			s.st.Targets = append(s.st.Targets[:idx], s.st.Targets[idx+1:]...)
		}
	}
	delete(s.running, id)
	delete(s.ops, opID)
	err := s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.cfg.DataDir, "packages", appname))
}

// Rollback reinstalls the last-known-good package.
func (s *Service) Rollback(id string) (string, error) {
	s.mu.Lock()
	t := s.findLocked(id)
	if t == nil {
		s.mu.Unlock()
		return "", errNotFound
	}
	if t.Runtime.LastKnownGoodPath == "" {
		s.mu.Unlock()
		return "", &cli.Error{Code: "no_rollback", Message: "没有可回滚的上一版安装包"}
	}
	if s.running[id] || (t.Runtime.LastOperation != nil && t.Runtime.LastOperation.Status == state.OpRunning) {
		s.mu.Unlock()
		return "", errConflict
	}
	opID := newID()
	now := s.now()
	t.Runtime.LastOperation = &state.Operation{ID: opID, Kind: "rollback", Status: state.OpRunning, Step: "rollback", StartedAt: now}
	t.Runtime.State = state.StateBuilding
	t.Runtime.LastError = ""
	t.Runtime.UpdatedAt = now
	s.running[id] = true
	s.ops[opID] = id
	pkg := t.Runtime.LastKnownGoodPath
	specCopy := t.Spec
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		delete(s.running, id)
		delete(s.ops, opID)
		return "", err
	}
	s.mu.Unlock()

	go func() {
		s.opMu.Lock()
		defer s.opMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
		defer cancel()
		err := s.runRollback(ctx, id, opID, specCopy, pkg)
		s.finishOperation(id, opID, err)
	}()
	return opID, nil
}

func (s *Service) runRollback(ctx context.Context, id, _ string, sp spec.ProxySpec, pkg string) error {
	app, err := s.findApp(ctx, sp.AppName)
	if err != nil {
		return err
	}
	if app != nil {
		if err := s.removeForReplace(ctx, sp.AppName, app.Status); err != nil {
			return err
		}
	}
	installCtx, cancel := context.WithTimeout(ctx, cliInstallTimeout)
	_, err = s.cli.Install(installCtx, sp.InstallVolume, pkg)
	cancel()
	if err != nil {
		return err
	}
	if err := s.waitStarted(ctx, sp.AppName); err != nil {
		return err
	}
	s.mu.Lock()
	if t := s.findLocked(id); t != nil {
		t.Runtime.State = state.StateRunning
		t.Runtime.PackagePath = pkg
		t.Runtime.NeedsReapply = false
		t.Runtime.LastError = ""
		t.Runtime.UpdatedAt = s.now()
	}
	err = s.saveLocked()
	s.mu.Unlock()
	return err
}

type specError struct {
	Field   string
	Message string
}

func (e *specError) Error() string { return e.Field + ": " + e.Message }

func checkTargetGuard(ctx context.Context, sp spec.ProxySpec) error {
	u, err := url.Parse(sp.TargetURL)
	if err != nil {
		return err
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		if _, err := fmt.Sscanf(p, "%d", &port); err != nil {
			return err
		}
	}
	g := guard.Guard{AllowLoopback: sp.AllowLoopback}
	return g.CheckHost(ctx, u.Hostname(), port)
}
