package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func postJSON(t *testing.T, s *Service, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := s.Handler(http.FileServerFS(UI()))
	req := httptest.NewRequest(http.MethodPost, "http://example.com"+path, strings.NewReader(body))
	req.Header.Set("X-CSRF-Token", s.Token())
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// importBody wraps a raw JSON document the way the browser sends the file text.
func importBody(doc string, extra string) string {
	body := `{"text": ` + strconv.Quote(doc)
	if extra != "" {
		body += ", " + extra
	}
	return body + "}"
}

func TestAPIImportPreviewCountsCreatesAndSkips(t *testing.T) {
	s, _ := newTestService(t)
	if _, err := s.Create(input()); err != nil {
		t.Fatal(err)
	}
	doc := `{"targets": [
    {"appname": "proxy.a", "display_name": "A", "target_url": "http://192.168.1.20:8070", "mode": "reverse"},
    {"appname": "proxy.b", "display_name": "B", "target_url": "http://192.168.1.21:8070", "mode": "reverse"},
    {"appname": "proxy.c", "display_name": "C", "target_url": "http://192.168.1.22:8070", "mode": "reverse"},
    {"appname": "daili.demo", "display_name": "Dup", "target_url": "http://192.168.1.23:8070", "mode": "reverse"}
  ]}`
	rec := postJSON(t, s, "/api/import/preview", importBody(doc, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	var plan ImportPlan
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Create != 3 || plan.Skip != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Rows[3].Action != ImportSkip {
		t.Fatalf("conflict row = %+v", plan.Rows[3])
	}
	if got := len(s.Batches()); got != 0 {
		t.Fatalf("preview created batches: %d", got)
	}
}

func TestAPIImportExecutesBatch(t *testing.T) {
	s, fake := newTestService(t)
	doc := `[{"appname": "proxy.x", "display_name": "X", "target_url": "http://192.168.1.30:8070", "mode": "direct"}]`
	rec := postJSON(t, s, "/api/import", importBody(doc, `"install": false`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	var view BatchView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Total != 1 || view.Status != "done" || view.Items[0].AppName != "proxy.x" {
		t.Fatalf("view = %+v", view)
	}
	if _, ok := fake.apps["proxy.x"]; ok {
		t.Fatal("install ran with install=false")
	}
	h := s.Handler(http.FileServerFS(UI()))
	get := httptest.NewRequest(http.MethodGet, "http://example.com/api/import/"+view.ID, nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, get)
	if getRec.Code != http.StatusOK {
		t.Fatalf("batch status = %d", getRec.Code)
	}
	var again BatchView
	if err := json.Unmarshal(getRec.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if again.ID != view.ID || again.Items[0].ID != view.Items[0].ID {
		t.Fatalf("batch reload = %+v", again)
	}
}

func TestAPIImportRejectsGarbage(t *testing.T) {
	s, _ := newTestService(t)
	rec := postJSON(t, s, "/api/import", importBody("not json", ""))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	rec = postJSON(t, s, "/api/import", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty status = %d body = %s", rec.Code, rec.Body)
	}
}

func TestAPIImportRequiresCSRF(t *testing.T) {
	s, _ := newTestService(t)
	h := s.Handler(http.FileServerFS(UI()))
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/import/preview", strings.NewReader(`{"text": "[]"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}
