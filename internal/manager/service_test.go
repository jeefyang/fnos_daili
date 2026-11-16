package manager

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jeefyang/fnos_daili/internal/assets"
	"github.com/jeefyang/fnos_daili/internal/cli"
	"github.com/jeefyang/fnos_daili/internal/fpk"
	"github.com/jeefyang/fnos_daili/internal/state"
)

func testELF() []byte {
	b := make([]byte, 64+56)
	copy(b[:4], []byte{0x7f, 'E', 'L', 'F'})
	b[4], b[5], b[6] = 2, 1, 1
	binary.LittleEndian.PutUint16(b[16:18], 2)
	binary.LittleEndian.PutUint16(b[18:20], 62)
	binary.LittleEndian.PutUint32(b[20:24], 1)
	binary.LittleEndian.PutUint64(b[32:40], 64)
	binary.LittleEndian.PutUint16(b[52:54], 64)
	binary.LittleEndian.PutUint16(b[54:56], 56)
	binary.LittleEndian.PutUint16(b[56:58], 1)
	binary.LittleEndian.PutUint32(b[64:68], 1)
	return b
}

type fakeCLI struct {
	mu          sync.Mutex
	apps        map[string]string
	installGate chan struct{}
}

func (f *fakeCLI) Run(ctx context.Context, dir string, args ...string) (cli.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(args) == 0 {
		return cli.Result{}, nil
	}
	switch args[0] {
	case "list":
		var names []string
		for name := range f.apps {
			names = append(names, name)
		}
		sort.Strings(names)
		var b strings.Builder
		b.WriteString("| APP NAME | DISPLAY NAME | VERSION | STATUS |\n")
		for _, name := range names {
			fmt.Fprintf(&b, "| %s | Demo | 1.0.0 | %s |\n", name, f.apps[name])
		}
		return cli.Result{Stdout: b.String()}, nil
	case "install-fpk":
		gate := f.installGate
		f.mu.Unlock()
		if gate != nil {
			<-gate
		}
		f.mu.Lock()
		v, err := fpk.VerifyFile(args[len(args)-1])
		if err != nil {
			return cli.Result{Stderr: err.Error(), ExitCode: 1}, fmt.Errorf("bad fpk: %w", err)
		}
		f.apps[v.AppName] = "running"
		return cli.Result{Stdout: "[Info]Installation complete.\n"}, nil
	case "uninstall":
		delete(f.apps, args[1])
		return cli.Result{Stdout: "Application [" + args[1] + "] uninstall success\n"}, nil
	case "start":
		f.apps[args[1]] = "running"
		return cli.Result{Stdout: "Launching complete\n"}, nil
	case "stop":
		f.apps[args[1]] = "stopped"
		return cli.Result{Stdout: "stop success\n"}, nil
	case "status":
		return cli.Result{Stdout: f.apps[args[1]] + "\n"}, nil
	case "default-volume":
		return cli.Result{Stdout: "1\n"}, nil
	}
	return cli.Result{}, nil
}

func newTestService(t *testing.T) (*Service, *fakeCLI) {
	t.Helper()
	cfg := Config{
		DataDir:     t.TempDir(),
		DeskDir:     t.TempDir(),
		Version:     "1.0.0",
		Platform:    "x86",
		CLIPath:     "appcenter-cli",
		ProxyBinary: testELF(),
		Icon64:      assets.Icon64,
		Icon256:     assets.Icon256,
		PollEvery:   time.Millisecond,
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeCLI{apps: map[string]string{}}
	s.cli.Runner = fake
	return s, fake
}

func input() SpecInput {
	return SpecInput{
		AppName:       "daili.demo",
		DisplayName:   "示例代理",
		TargetURL:     "http://192.168.1.10:8123",
		Mode:          "reverse",
		InstallVolume: "1",
	}
}

func TestCreateAndUpdate(t *testing.T) {
	s, _ := newTestService(t)
	got, err := s.Create(input())
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime.State != state.StateDraft || got.Spec.ID == "" {
		t.Fatalf("created = %+v", got)
	}
	in := input()
	in.DisplayName = "改过的名字"
	upd, err := s.Update(got.Spec.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if upd.Spec.AppName != "daili.demo" || upd.Spec.DisplayName != "改过的名字" {
		t.Fatalf("updated = %+v", upd)
	}
	in.AppName = "daili.other"
	if _, err := s.Update(got.Spec.ID, in); err == nil {
		t.Fatal("appname change was accepted")
	}
}

func TestCreateHashesPasswordAndNeverReturnsIt(t *testing.T) {
	s, _ := newTestService(t)
	in := input()
	in.BasicAuth = &AuthInput{Username: "admin", Password: "correct horse"}
	got, err := s.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.BasicAuth == nil || !got.Spec.BasicAuth.HasPassword || got.Spec.BasicAuth.Username != "admin" {
		t.Fatalf("auth view = %+v", got.Spec.BasicAuth)
	}
	stored := s.store
	raw, err := os.ReadFile(stored.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "correct horse") {
		t.Fatal("plaintext password reached state.json")
	}
}

func waitOp(t *testing.T, s *Service, id, opID string) state.Operation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if op, ok := s.Operation(opID); ok && op.Status != state.OpRunning {
			return op
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("operation %s did not finish", opID)
	return state.Operation{}
}

func TestApplyInstallsAndDeleteRemoves(t *testing.T) {
	s, fake := newTestService(t)
	got, err := s.Create(input())
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.Apply(got.Spec.ID, "install")
	if err != nil {
		t.Fatal(err)
	}
	if done := waitOp(t, s, got.Spec.ID, op); done.Status != state.OpSucceeded {
		t.Fatalf("operation = %+v", done)
	}
	v, _ := s.Get(got.Spec.ID)
	if v.Runtime.State != state.StateRunning || v.Runtime.PackagePath == "" {
		t.Fatalf("runtime = %+v", v.Runtime)
	}
	if _, err := os.Stat(v.Runtime.PackagePath); err != nil {
		t.Fatalf("package path: %v", err)
	}
	fake.mu.Lock()
	if fake.apps["daili.demo"] != "running" {
		t.Fatalf("installed apps = %v", fake.apps)
	}
	fake.mu.Unlock()
	del, err := s.Delete(got.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitOp(t, s, got.Spec.ID, del)
	if _, ok := s.Get(got.Spec.ID); ok {
		t.Fatal("target still exists after delete")
	}
	if _, err := os.Stat(filepath.Join(s.DataDir(), "packages", "daili.demo")); !os.IsNotExist(err) {
		t.Fatalf("package dir still exists: %v", err)
	}
}

func TestDeleteDraftWithoutInstalledAppRemovesState(t *testing.T) {
	s, _ := newTestService(t)
	got, err := s.Create(input())
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.Delete(got.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitOp(t, s, got.Spec.ID, op); done.Status != state.OpSucceeded {
		t.Fatalf("operation = %+v", done)
	}
	if _, ok := s.Get(got.Spec.ID); ok {
		t.Fatal("draft target still exists after delete")
	}
}

func TestReconcileDoesNotInterruptLiveOperation(t *testing.T) {
	s, fake := newTestService(t)
	got, err := s.Create(input())
	if err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	fake.mu.Lock()
	fake.installGate = gate
	fake.mu.Unlock()
	op, err := s.Apply(got.Spec.ID, "install")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := s.running[got.Spec.ID]; ok {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if _, ok := s.running[got.Spec.ID]; !ok {
		t.Fatal("operation did not start")
	}
	s.Reconcile(context.Background())
	v, _ := s.Get(got.Spec.ID)
	if v.Runtime.State == state.StateMissing {
		t.Fatalf("live target marked missing: %+v", v.Runtime)
	}
	if v.Runtime.LastOperation == nil || v.Runtime.LastOperation.Status != state.OpRunning {
		t.Fatalf("live operation was interrupted: %+v", v.Runtime.LastOperation)
	}
	close(gate)
	if done := waitOp(t, s, got.Spec.ID, op); done.Status != state.OpSucceeded {
		t.Fatalf("operation = %+v", done)
	}
}

func TestListIncludesUnmanagedAfterReconcile(t *testing.T) {
	s, fake := newTestService(t)
	fake.mu.Lock()
	fake.apps["other.app"] = "running"
	fake.mu.Unlock()
	s.Reconcile(context.Background())
	list := s.List()
	if len(list.Unmanaged) != 1 || list.Unmanaged[0].AppName != "other.app" {
		t.Fatalf("unmanaged = %+v", list.Unmanaged)
	}
}
