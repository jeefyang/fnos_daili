package manager

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportDocumentCarriesIconAndAuth(t *testing.T) {
	s, _ := newTestService(t)
	in := input()
	in.BasicAuth = &AuthInput{Username: "alice", Password: "correct-horse"}
	in.IconData = testPNGDataURL(t, 200, 120)
	created, err := s.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.Export(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Schema != 1 || len(doc.Targets) != 1 {
		t.Fatalf("doc = %+v", doc)
	}
	row := doc.Targets[0]
	if row.AppName != created.Spec.AppName || row.Port != created.Spec.Port || row.State != created.Runtime.State {
		t.Fatalf("row = %+v", row)
	}
	if row.IconSource != "custom" || !strings.HasPrefix(row.Icon, "data:image/png;base64,") {
		t.Fatalf("icon = %q source = %q", truncate(row.Icon, 40), row.IconSource)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(row.Icon, "data:image/png;base64,"))
	if err != nil || len(raw) == 0 {
		t.Fatalf("icon bytes: %v", err)
	}
	if row.BasicAuth == nil || row.BasicAuth.Username != "alice" || !strings.HasPrefix(row.BasicAuth.PasswordHash, "$2") {
		t.Fatalf("basic auth = %+v", row.BasicAuth)
	}
}

func TestExportWithoutIconsKeepsSource(t *testing.T) {
	s, _ := newTestService(t)
	in := input()
	in.IconData = testPNGDataURL(t, 128, 128)
	if _, err := s.Create(in); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Export(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	row := doc.Targets[0]
	if row.Icon != "" || row.IconSource != "custom" {
		t.Fatalf("row = %+v", row)
	}
}

func TestExportRejectsUnknownID(t *testing.T) {
	s, _ := newTestService(t)
	if _, err := s.Create(input()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Export([]string{"nope"}, true); err == nil {
		t.Fatal("unknown id exported")
	}
}

func TestExportImportRoundTripKeepsIconAndHash(t *testing.T) {
	s, _ := newTestService(t)
	in := input()
	in.DNSServer = "223.5.5.5"
	in.BasicAuth = &AuthInput{Username: "alice", Password: "correct-horse"}
	in.IconData = testPNGDataURL(t, 200, 120)
	if _, err := s.Create(in); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Export(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := parseImportDocument(blob)
	if err != nil {
		t.Fatalf("exported file does not import: %v", err)
	}
	if len(parsed.Targets) != 1 || parsed.Targets[0].Icon == "" {
		t.Fatalf("parsed items = %+v", parsed.Targets)
	}
	if parsed.Settings == nil {
		t.Fatal("full export did not carry the settings block")
	}

	fresh, _ := newTestService(t)
	plan, _, _ := fresh.PlanImport(context.Background(), parsed, "")
	if plan.Create != 1 || plan.Rows[0].IconSource != "custom" {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := fresh.ImportBulk(context.Background(), parsed, false, true, false); err != nil {
		t.Fatal(err)
	}
	again, err := fresh.Export(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	row := again.Targets[0]
	if row.IconSource != "custom" || row.Icon == "" {
		t.Fatalf("icon lost in round trip: %+v", row)
	}
	if row.BasicAuth == nil || row.BasicAuth.PasswordHash != doc.Targets[0].BasicAuth.PasswordHash {
		t.Fatalf("hash changed: %+v vs %+v", row.BasicAuth, doc.Targets[0].BasicAuth)
	}
	if row.DNSServer != "223.5.5.5" {
		t.Fatalf("dns server = %q", row.DNSServer)
	}
}

func TestImportIconFromAbsolutePath(t *testing.T) {
	s, _ := newTestService(t)
	dataURL := testPNGDataURL(t, 150, 150)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, "data:image/png;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "logo.png")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	in := input()
	in.Icon = path
	created, err := s.Create(in)
	if err != nil {
		t.Fatalf("create with icon path: %v", err)
	}
	if !created.Spec.HasIcon {
		t.Fatal("icon path was ignored")
	}

	bad := input()
	bad.AppName = "proxy.relative"
	bad.Icon = "icons/logo.png"
	if _, err := s.Create(bad); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative path error = %v", err)
	}

	missing := input()
	missing.AppName = "proxy.missing"
	missing.Icon = filepath.Join(t.TempDir(), "nope.png")
	if _, err := s.Create(missing); err == nil {
		t.Fatal("missing icon file accepted")
	}
}

func TestImportSkipsRowsWithBadIcon(t *testing.T) {
	s, _ := newTestService(t)
	plan, _, _ := s.PlanImport(context.Background(), importDocument{Targets: []SpecInput{
		{AppName: "proxy.bad", DisplayName: "Bad", TargetURL: "http://192.168.1.30:8080", Mode: "reverse", Icon: "/definitely/missing.png"},
	}}, "")
	if plan.Skip != 1 || !strings.Contains(plan.Rows[0].Reason, "图标") {
		t.Fatalf("plan = %+v", plan)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
