package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jeefyang/fnos_daili/internal/spec"
)

func sampleState() State {
	st := New()
	st.Targets = append(st.Targets, Target{
		Spec:    spec.ProxySpec{ID: "id-1", AppName: "proxy.home.9f3a", DisplayName: "Home", TargetURL: "http://192.168.1.10:8123", Mode: "reverse"},
		Runtime: Runtime{State: StateDraft},
	})
	return st
}

func TestLoadMissingIsEmpty(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "state.json")}
	st, warning, err := s.Load()
	if err != nil || warning != "" {
		t.Fatalf("Load() = %v, %q", err, warning)
	}
	if st.Schema != Schema || len(st.Targets) != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := Store{Path: path}
	if err := s.Save(sampleState()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("state mode = %o, want no group/other bits", info.Mode().Perm())
	}
	got, warning, err := s.Load()
	if err != nil || warning != "" {
		t.Fatalf("Load() = %v, %q", err, warning)
	}
	if len(got.Targets) != 1 || got.Targets[0].Spec.AppName != "proxy.home.9f3a" {
		t.Fatalf("loaded = %+v", got)
	}
}

func TestLoadCorruptBacksUpAndReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, warning, err := Store{Path: path}.Load()
	if err != nil {
		t.Fatal(err)
	}
	if warning == "" || !strings.Contains(warning, "corrupt") {
		t.Fatalf("warning = %q", warning)
	}
	if len(st.Targets) != 0 {
		t.Fatalf("state = %+v", st)
	}
	matches, _ := filepath.Glob(path + ".bad.*")
	if len(matches) != 1 {
		t.Fatalf("backup files = %v", matches)
	}
}

func TestLoadRejectsUnknownSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	b, _ := json.Marshal(State{Schema: 99})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	st, warning, err := Store{Path: path}.Load()
	if err != nil {
		t.Fatal(err)
	}
	if warning == "" || st.Schema != Schema {
		t.Fatalf("state = %+v warning = %q", st, warning)
	}
}
