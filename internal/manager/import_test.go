package manager

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jeefyang/fnos_daili/internal/state"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitBatch(t *testing.T, s *Service, id string) BatchView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		v, ok := s.Batch(id)
		if !ok {
			t.Fatalf("batch %s missing", id)
		}
		if v.Status == "done" {
			return v
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("batch %s did not finish", id)
	return BatchView{}
}

func TestParseImportDocumentAcceptsArrayAndObject(t *testing.T) {
	arr, err := parseImportDocument([]byte(`[{"target_url":"http://192.168.1.10:8123"}]`))
	if err != nil || len(arr) != 1 || arr[0].TargetURL != "http://192.168.1.10:8123" {
		t.Fatalf("array form: %v %+v", err, arr)
	}
	obj, err := parseImportDocument([]byte(`{"targets":[{"target_url":"http://a.local:80"},{"target_url":"http://b.local:81"}]}`))
	if err != nil || len(obj) != 2 {
		t.Fatalf("object form: %v %+v", err, obj)
	}
	if _, err := parseImportDocument([]byte("   ")); err == nil {
		t.Fatal("empty document accepted")
	}
	if _, err := parseImportDocument([]byte(`{"foo":1}`)); err == nil {
		t.Fatal("document without targets accepted")
	}
}

func TestPlanImportMarksConflictsAndBadRows(t *testing.T) {
	s, _ := newTestService(t)
	if _, err := s.Create(input()); err != nil {
		t.Fatal(err)
	}
	items := []SpecInput{
		{AppName: "proxy.a", DisplayName: "A", TargetURL: "http://192.168.1.20:8070", Mode: "reverse"},
		{AppName: "proxy.b", DisplayName: "B", TargetURL: "http://192.168.1.21:8070", Mode: "reverse"},
		{AppName: "daili.demo", DisplayName: "dup", TargetURL: "http://192.168.1.22:8070", Mode: "reverse"},
		{AppName: "proxy.bad", DisplayName: "bad", TargetURL: "ftp://192.168.1.23", Mode: "reverse"},
		{AppName: "proxy.b", DisplayName: "dup in file", TargetURL: "http://192.168.1.24:8070", Mode: "reverse"},
	}
	plan, specs, _ := s.PlanImport(context.Background(), items, "")
	if plan.Create != 2 || plan.Skip != 3 {
		t.Fatalf("plan create=%d skip=%d rows=%+v", plan.Create, plan.Skip, plan.Rows)
	}
	if plan.Rows[2].Action != ImportSkip || !strings.Contains(plan.Rows[2].Reason, "占用") {
		t.Fatalf("existing appname not skipped: %+v", plan.Rows[2])
	}
	if !strings.Contains(plan.Rows[3].Reason, "字段不合法") {
		t.Fatalf("bad row reason: %q", plan.Rows[3].Reason)
	}
	if plan.Rows[4].Action != ImportSkip || !strings.Contains(plan.Rows[4].Reason, "重复") {
		t.Fatalf("duplicate row: %+v", plan.Rows[4])
	}
	if specs[0].AppName != "proxy.a" || specs[1].AppName != "proxy.b" {
		t.Fatalf("specs not normalized: %+v", specs)
	}
}

func TestPlanImportSuggestsAppName(t *testing.T) {
	s, _ := newTestService(t)
	plan, specs, _ := s.PlanImport(context.Background(), []SpecInput{
		{DisplayName: "Home Assistant", TargetURL: "http://192.168.1.10:8123", Mode: "reverse"},
	}, "")
	if plan.Create != 1 {
		t.Fatalf("plan: %+v", plan)
	}
	if specs[0].AppName != "proxy.home-assistant" {
		t.Fatalf("suggested appname = %q", specs[0].AppName)
	}
}

func TestImportBulkInstallsReachableTargets(t *testing.T) {
	s, fake := newTestService(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	target := "http://127.0.0.1:" + itoa(ln.Addr().(*net.TCPAddr).Port)
	items := []SpecInput{
		{AppName: "proxy.one", DisplayName: "One", TargetURL: target, Mode: "reverse", AllowLoopback: true},
		{AppName: "proxy.two", DisplayName: "Two", TargetURL: target, Mode: "reverse", AllowLoopback: true},
	}
	view, err := s.ImportBulk(context.Background(), items, true, false)
	if err != nil {
		t.Fatal(err)
	}
	done := waitBatch(t, s, view.ID)
	if done.Total != 2 || done.Done != 2 || done.Failed != 0 {
		t.Fatalf("batch result: %+v", done)
	}
	for _, it := range done.Items {
		if it.State != state.StateRunning || it.OperationID == "" {
			t.Fatalf("item not running: %+v", it)
		}
		if fake.apps[it.AppName] != "running" {
			t.Fatalf("cli did not install %s", it.AppName)
		}
	}
}

func TestImportBulkKeepsUnreachableTargetAsDraft(t *testing.T) {
	s, fake := newTestService(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	good := "http://127.0.0.1:" + itoa(ln.Addr().(*net.TCPAddr).Port)
	dead := "http://127.0.0.1:" + itoa(freePort(t))
	items := []SpecInput{
		{AppName: "proxy.dead", DisplayName: "Dead", TargetURL: dead, Mode: "reverse", AllowLoopback: true},
		{AppName: "proxy.good", DisplayName: "Good", TargetURL: good, Mode: "reverse", AllowLoopback: true},
	}
	view, err := s.ImportBulk(context.Background(), items, true, false)
	if err != nil {
		t.Fatal(err)
	}
	done := waitBatch(t, s, view.ID)
	if done.Failed != 1 || done.Done != 2 {
		t.Fatalf("batch: %+v", done)
	}
	deadItem, goodItem := done.Items[0], done.Items[1]
	if deadItem.State != state.StateDraft || !strings.Contains(deadItem.Error, "目标不可达") {
		t.Fatalf("dead item: %+v", deadItem)
	}
	if goodItem.State != state.StateRunning {
		t.Fatalf("good item: %+v", goodItem)
	}
	if _, ok := fake.apps["proxy.dead"]; ok {
		t.Fatal("unreachable target was installed")
	}
	ln.Close()
}

func TestImportBulkLeavesExistingTargetsAlone(t *testing.T) {
	s, _ := newTestService(t)
	existing, err := s.Create(input())
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.ImportBulk(context.Background(), []SpecInput{
		{AppName: existing.Spec.AppName, DisplayName: "override", TargetURL: "http://192.168.1.99:9999", Mode: "reverse"},
	}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != 0 || len(view.Skipped) != 1 {
		t.Fatalf("conflict not skipped: %+v", view)
	}
	cur, ok := s.getTarget(existing.Spec.ID)
	if !ok {
		t.Fatal("existing target removed")
	}
	if cur.Spec.TargetURL != existing.Spec.TargetURL || cur.Spec.Port != existing.Spec.Port {
		t.Fatalf("existing target changed: %+v", cur.Spec)
	}
}

func TestImportBulkWithoutInstallKeepsDrafts(t *testing.T) {
	s, fake := newTestService(t)
	view, err := s.ImportBulk(context.Background(), []SpecInput{
		{AppName: "proxy.draft", DisplayName: "Draft", TargetURL: "http://192.168.1.30:9000", Mode: "reverse"},
	}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if view.Total != 1 || view.Status != "done" {
		t.Fatalf("view: %+v", view)
	}
	if _, ok := fake.apps["proxy.draft"]; ok {
		t.Fatal("install ran with install=false")
	}
	tgt, _ := s.getTarget(view.Items[0].ID)
	if tgt.Runtime.State != state.StateDraft {
		t.Fatalf("state = %s", tgt.Runtime.State)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
