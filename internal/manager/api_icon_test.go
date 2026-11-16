package manager

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testPNGDataURL(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x % 255), G: uint8(y % 255), B: 160, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestAPIIconUploadAndRemoval(t *testing.T) {
	s, _ := newTestService(t)
	h := s.Handler(http.FileServerFS(UI()))
	body, err := json.Marshal(map[string]any{
		"appname":      "daili.demo",
		"display_name": "Demo",
		"target_url":   "http://192.168.1.10:8123",
		"mode":         "reverse",
		"icon_data":    testPNGDataURL(t, 200, 120),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://example.com/api/targets", bytes.NewReader(body))
	req.Header.Set("X-CSRF-Token", s.Token())
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body)
	}
	var got TargetView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Spec.HasIcon {
		t.Fatal("created target does not report has_icon")
	}
	iconURL := "http://example.com/api/targets/" + got.Spec.ID + "/icon"
	req = httptest.NewRequest(http.MethodGet, iconURL, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Body.Len() == 0 {
		t.Fatalf("icon status = %d type = %q len = %d", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	req = httptest.NewRequest(http.MethodGet, "http://example.com/api/default-icon", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("default icon status = %d", rec.Code)
	}

	update, err := json.Marshal(map[string]any{
		"appname":      "daili.demo",
		"display_name": "Demo",
		"target_url":   "http://192.168.1.10:8123",
		"mode":         "reverse",
		"remove_icon":  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPut, "http://example.com/api/targets/"+got.Spec.ID, bytes.NewReader(update))
	req.Header.Set("X-CSRF-Token", s.Token())
	req.Header.Set("Origin", "http://example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body = %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.HasIcon {
		t.Fatal("has_icon still true after remove_icon")
	}
	req = httptest.NewRequest(http.MethodGet, iconURL, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("icon after removal status = %d", rec.Code)
	}
}
