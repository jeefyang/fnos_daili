package manager

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jeefyang/fnos_daili/internal/assets"
)

func TestAPIAppIconServesInstalledAppIcon(t *testing.T) {
	s, _ := newTestService(t)
	root := t.TempDir()
	s.cfg.InstallRoots = []string{root}

	dir := filepath.Join(root, "watchcow.demo", "ui", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "icon_dashboard_256.png"), assets.Icon256, 0o644); err != nil {
		t.Fatal(err)
	}

	h := s.Handler(http.FileServerFS(UI()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com/api/apps/watchcow.demo/icon", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content type = %q", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), assets.Icon256) {
		t.Fatalf("icon body = %d bytes, want %d", rec.Body.Len(), len(assets.Icon256))
	}

	// Unknown apps and invalid names must not turn into path traversal.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com/api/apps/watchcow.absent/icon", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing app status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com/api/apps/..%2Fetc%2Fpasswd/icon", nil))
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Fatalf("traversal status = %d", rec.Code)
	}
}

func TestAPIPackageZipBundlesSelectedPackages(t *testing.T) {
	s, _ := newTestService(t)
	view, err := s.Create(input())
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.Apply(view.Spec.ID, "install")
	if err != nil {
		t.Fatal(err)
	}
	if done := waitOp(t, s, view.Spec.ID, op); done.Status != "succeeded" {
		t.Fatalf("operation = %+v", done)
	}

	h := s.Handler(http.FileServerFS(UI()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com/api/packages?ids="+view.Spec.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("content type = %q", got)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, ".zip") {
		t.Fatalf("content disposition = %q", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || !strings.HasSuffix(zr.File[0].Name, ".fpk") {
		names := make([]string, 0, len(zr.File))
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		t.Fatalf("zip entries = %v", names)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	n, _ := io.Copy(io.Discard, rc)
	rc.Close()
	if n < 1024 {
		t.Fatalf("package in zip is %d bytes", n)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com/api/packages?ids=missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing package status = %d", rec.Code)
	}
}
