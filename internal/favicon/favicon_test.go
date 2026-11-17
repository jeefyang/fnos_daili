package favicon

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func pngBytes(t *testing.T, side int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 10, G: byte(x), B: byte(y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// icoWithPNGFrame wraps a PNG in an ICO container, which is how modern sites
// ship multi-size favicons.
func icoWithPNGFrame(t *testing.T, side int) []byte {
	t.Helper()
	payload := pngBytes(t, side)
	header := make([]byte, 6)
	binary.LittleEndian.PutUint16(header[2:4], 1)
	binary.LittleEndian.PutUint16(header[4:6], 1)
	entry := make([]byte, 16)
	if side < 256 {
		entry[0] = byte(side)
		entry[1] = byte(side)
	}
	binary.LittleEndian.PutUint16(entry[4:6], 1)
	binary.LittleEndian.PutUint16(entry[6:8], 32)
	binary.LittleEndian.PutUint32(entry[8:12], uint32(len(payload)))
	binary.LittleEndian.PutUint32(entry[12:16], 22)
	var buf bytes.Buffer
	buf.Write(header)
	buf.Write(entry)
	buf.Write(payload)
	return buf.Bytes()
}

func TestFetchLinkIcon(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><link rel=\"icon\" href=\"/assets/i.png\" sizes=\"32x32\"></head></html>"))
	})
	mux.HandleFunc("/assets/i.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes(t, 32))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	set, err := Fetch(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Icon256) == 0 || len(set.Icon64) == 0 {
		t.Fatal("empty icon set")
	}
}

func TestFetchPrefersDeclaredSize(t *testing.T) {
	var mu sync.Mutex
	var order []string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<link rel=\"icon\" href=\"/small.png\"><link rel=\"icon\" href=\"/big.png\" sizes=\"256x256\">"))
	})
	serve := func(path string, side int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			order = append(order, path)
			mu.Unlock()
			_, _ = w.Write(pngBytes(t, side))
		}
	}
	mux.HandleFunc("/small.png", serve("/small.png", 16))
	mux.HandleFunc("/big.png", serve("/big.png", 256))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := Fetch(context.Background(), srv.Client(), srv.URL); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) == 0 || order[0] != "/big.png" {
		t.Fatalf("fetch order = %v, want /big.png first", order)
	}
}

func TestFetchFallsBackToFaviconICO(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><head><title>no links</title></head></html>"))
	})
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(icoWithPNGFrame(t, 64))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := Fetch(context.Background(), srv.Client(), srv.URL); err != nil {
		t.Fatal(err)
	}
}

func TestFetchSingleQuotedAttributes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<link href='/i.png' rel='shortcut icon'>"))
	})
	mux.HandleFunc("/i.png", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(pngBytes(t, 48))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := Fetch(context.Background(), srv.Client(), srv.URL); err != nil {
		t.Fatal(err)
	}
}

func TestFetchNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/favicon.ico") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("<html><head></head></html>"))
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), srv.Client(), srv.URL)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestFetchRejectsOversizeImage(t *testing.T) {
	big := bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, MaxIconBytes/4+16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/favicon.ico") {
			_, _ = w.Write(big)
			return
		}
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), srv.Client(), srv.URL)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestFetchSkipsUnusableCandidate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<link rel=\"icon\" href=\"/bad.png\"><link rel=\"icon\" href=\"/good.png\">"))
	})
	mux.HandleFunc("/bad.png", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not an image"))
	})
	mux.HandleFunc("/good.png", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(pngBytes(t, 64))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := Fetch(context.Background(), srv.Client(), srv.URL); err != nil {
		t.Fatal(err)
	}
}

func TestGetErrorHidesQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, err := get(context.Background(), srv.Client(), srv.URL+"/x.png?token=abc", 1024, "image/*")
	if err == nil {
		t.Fatal("404 accepted")
	}
	if strings.Contains(err.Error(), "abc") {
		t.Fatalf("error leaks query: %v", err)
	}
	if !strings.Contains(err.Error(), "/x.png") {
		t.Fatalf("error lost the path: %v", err)
	}
}

func TestGetTransportErrorHidesQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	target := srv.URL + "/x.png?token=abc"
	srv.Close()
	_, err := get(context.Background(), srv.Client(), target, 1024, "image/*")
	if err == nil {
		t.Fatal("closed server accepted")
	}
	if strings.Contains(err.Error(), "abc") {
		t.Fatalf("transport error leaks query: %v", err)
	}
}

func TestRedactURL(t *testing.T) {
	got := redactURL("https://user:pw@example.com:8443/a/b.png?x=1#f")
	if got != "https://example.com:8443/a/b.png" {
		t.Fatalf("redactURL = %q", got)
	}
}

func TestFetchRejectsInvalidOrigin(t *testing.T) {
	if _, err := Fetch(context.Background(), http.DefaultClient, "ftp://x"); err == nil {
		t.Fatal("ftp origin accepted")
	}
	if _, err := Fetch(context.Background(), nil, "http://x"); err == nil {
		t.Fatal("nil client accepted")
	}
}
