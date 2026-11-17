package manager

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func libraryPNG(t *testing.T, side int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 0x1e, G: 0x88, B: 0xe5, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAPIIconLibraryFetchesFromLibrary(t *testing.T) {
	lib := http.NewServeMux()
	lib.HandleFunc("/home-assistant.png", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(libraryPNG(t, 512))
	})
	srv := httptest.NewServer(lib)
	defer srv.Close()

	s, _ := newTestService(t)
	s.cfg.IconLibraryBase = srv.URL
	h := s.Handler(http.FileServerFS(UI()))

	body, _ := json.Marshal(map[string]any{
		"appname":      "proxy.ha.test",
		"display_name": "Home Assistant",
	})
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/icon-library", bytes.NewReader(body))
	req.Header.Set("X-CSRF-Token", s.Token())
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	var got struct {
		IconData string `json:"icon_data"`
		Slug     string `json:"slug"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Slug != "home-assistant" {
		t.Fatalf("slug = %q, want home-assistant", got.Slug)
	}
	if !strings.HasPrefix(got.IconData, "data:image/png;base64,") || len(got.IconData) < 100 {
		t.Fatalf("unexpected icon_data: %.60q", got.IconData)
	}
}

func TestAPIIconLibraryNotFound(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	s, _ := newTestService(t)
	s.cfg.IconLibraryBase = srv.URL
	h := s.Handler(http.FileServerFS(UI()))

	body, _ := json.Marshal(map[string]any{"appname": "proxy.nope", "display_name": "Nope"})
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/icon-library", bytes.NewReader(body))
	req.Header.Set("X-CSRF-Token", s.Token())
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "icon_library_not_found") {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestAPIIconLibraryRequiresCSRF(t *testing.T) {
	s, _ := newTestService(t)
	h := s.Handler(http.FileServerFS(UI()))
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/icon-library", strings.NewReader(`{"appname":"x"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}
