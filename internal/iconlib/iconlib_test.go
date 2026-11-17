package iconlib

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func pngBytes(t *testing.T, side int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 0x20, G: byte(x), B: byte(y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Home Assistant": "home-assistant",
		"proxy.ha.test":  "proxy-ha-test",
		"  Jellyfin!! ":  "jellyfin",
		"immich":         "immich",
		"":               "",
		"中文":             "",
		"Portainer (CE)": "portainer-ce",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchUsesFirstAvailableName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/home-assistant.png", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(pngBytes(t, 512))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	set, slug, err := Fetch(context.Background(), srv.Client(), srv.URL, []string{"proxy.ha.test", "Home Assistant"})
	if err != nil {
		t.Fatal(err)
	}
	if slug != "home-assistant" {
		t.Fatalf("slug = %q, want home-assistant", slug)
	}
	if len(set.Icon256) == 0 {
		t.Fatal("empty icon")
	}
}

func TestFetchNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, _, err := Fetch(context.Background(), srv.Client(), srv.URL, []string{"nothing"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestFetchSkipsUnusableIcon(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/broken.png", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not an image"))
	})
	mux.HandleFunc("/good.png", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(pngBytes(t, 256))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	set, slug, err := Fetch(context.Background(), srv.Client(), srv.URL, []string{"broken", "good"})
	if err != nil {
		t.Fatal(err)
	}
	if slug != "good" || len(set.Icon256) == 0 {
		t.Fatalf("slug = %q, want good", slug)
	}
}

func TestFetchRejectsBadBase(t *testing.T) {
	if _, _, err := Fetch(context.Background(), http.DefaultClient, "not a url", []string{"x"}); err == nil {
		t.Fatal("expected an error for an invalid base")
	}
}
