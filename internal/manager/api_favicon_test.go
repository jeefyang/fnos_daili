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

func iconPNG(t *testing.T, side int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 20, G: byte(x), B: byte(y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func faviconSite(t *testing.T, withIcon bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if withIcon {
			_, _ = w.Write([]byte("<html><head><link rel='icon' href='/i.png'></head></html>"))
			return
		}
		_, _ = w.Write([]byte("<html><head></head></html>"))
	})
	mux.HandleFunc("/i.png", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(iconPNG(t, 64))
	})
	return httptest.NewServer(mux)
}

func postFavicon(t *testing.T, s *Service, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := s.Handler(http.FileServerFS(UI()))
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/favicon", strings.NewReader(body))
	req.Header.Set("X-CSRF-Token", s.Token())
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func faviconBody(t *testing.T, targetURL string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"target_url": targetURL, "allow_loopback": true})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestAPIFaviconFetchesFromTarget(t *testing.T) {
	site := faviconSite(t, true)
	defer site.Close()
	s, _ := newTestService(t)
	rec := postFavicon(t, s, faviconBody(t, site.URL))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	iconData, _ := got["icon_data"].(string)
	if !strings.HasPrefix(iconData, "data:image/png;base64,") {
		t.Fatalf("icon_data = %q", iconData)
	}
}

func TestAPIFaviconNotFound(t *testing.T) {
	site := faviconSite(t, false)
	defer site.Close()
	s, _ := newTestService(t)
	rec := postFavicon(t, s, faviconBody(t, site.URL))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "favicon_not_found") {
		t.Fatalf("body = %s", rec.Body)
	}
}

func TestAPIFaviconRejectsBadTarget(t *testing.T) {
	s, _ := newTestService(t)
	rec := postFavicon(t, s, faviconBody(t, "http://x/y"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}

func TestAPIFaviconRequiresCSRF(t *testing.T) {
	s, _ := newTestService(t)
	h := s.Handler(http.FileServerFS(UI()))
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/favicon", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body)
	}
}
