// Package iconlib looks an application icon up in a public icon library, the
// fallback watchcow uses when a site only ships a tiny favicon.
package iconlib

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/jeefyang/fnos_daili/internal/icon"
)

const (
	// DefaultBase is the homarr-labs/dashboard-icons PNG mirror on jsDelivr,
	// the same library watchcow falls back to.
	DefaultBase = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/png/"
	// MaxBytes limits one library download.
	MaxBytes    = icon.MaxUploadBytes
	userAgent   = "daili-manager/1.0 (+https://github.com/jeefyang/fnos_daili)"
	maxRedirect = 5
)

// ErrNotFound means the library answered but had no icon for any candidate.
var ErrNotFound = errors.New("no icon in the icon library")

// Slug turns a name into the dashboard-icons file stem: lowercase ASCII
// alphanumerics joined by single dashes.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// Fetch tries each name in order and returns the first icon the library has,
// together with the slug that matched.
func Fetch(ctx context.Context, client *http.Client, base string, names []string) (icon.Set, string, error) {
	var zero icon.Set
	if client == nil {
		return zero, "", errors.New("iconlib: nil http client")
	}
	baseURL, err := url.Parse(strings.TrimSpace(base))
	if err != nil || baseURL.Host == "" {
		return zero, "", fmt.Errorf("iconlib: invalid base URL %q", base)
	}
	if !strings.HasSuffix(baseURL.Path, "/") {
		baseURL.Path += "/"
	}
	seen := make(map[string]bool)
	var lastErr error
	tried := 0
	for _, name := range names {
		slug := Slug(name)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		tried++
		data, err := get(ctx, client, baseURL.ResolveReference(&url.URL{Path: slug + ".png"}).String())
		if err != nil {
			lastErr = err
			continue
		}
		set, err := icon.NormalizeFavicon(data)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", slug, err)
			continue
		}
		return set, slug, nil
	}
	if lastErr != nil {
		return zero, "", fmt.Errorf("%w (tried %d candidates; last error: %v)", ErrNotFound, tried, lastErr)
	}
	return zero, "", fmt.Errorf("%w (tried %d candidates)", ErrNotFound, tried)
}

func get(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "image/png,image/*;q=0.9,*/*;q=0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxBytes {
		return nil, fmt.Errorf("GET %s: body larger than %d bytes", rawURL, MaxBytes)
	}
	return data, nil
}
