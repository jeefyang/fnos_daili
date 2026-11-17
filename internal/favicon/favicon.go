// Package favicon discovers a site icon for a proxied target: it reads the
// target's HTML for <link rel="icon"> candidates and falls back to
// /favicon.ico, then normalizes whatever it finds into the two fnOS sizes.
package favicon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/jeefyang/fnos_daili/internal/icon"
)

const (
	// MaxHTMLBytes limits how much of the target page is read while looking
	// for icon links.
	MaxHTMLBytes = 256 << 10
	// MaxIconBytes limits a single icon download.
	MaxIconBytes = icon.MaxUploadBytes

	userAgent = "daili-manager/1.0 (+https://github.com/jeefyang/fnos_daili)"
)

// ErrNotFound means the target answered but offered no usable icon.
var ErrNotFound = errors.New("no site icon found")

// Fetch resolves the icon for origin (a normalized scheme://host[:port]) and
// returns it normalized to 64px and 256px PNGs. The caller supplies the HTTP
// client, which decides the resolver, the address guard, and TLS policy.
func Fetch(ctx context.Context, client *http.Client, origin string) (icon.Set, error) {
	var zero icon.Set
	if client == nil {
		return zero, errors.New("favicon: nil http client")
	}
	base, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(origin), "/") + "/")
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return zero, fmt.Errorf("favicon: invalid origin %q", origin)
	}
	var candidates []string
	page, pageErr := get(ctx, client, base.String(), MaxHTMLBytes, "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1")
	if pageErr == nil {
		candidates = append(candidates, iconLinks(page, base)...)
	}
	candidates = append(candidates, base.ResolveReference(&url.URL{Path: "/favicon.ico"}).String())

	var lastErr error
	tried := 0
	for _, cand := range candidates {
		data, err := get(ctx, client, cand, MaxIconBytes, "image/*")
		if err != nil {
			lastErr = err
			tried++
			continue
		}
		set, err := icon.NormalizeFavicon(data)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", redactURL(cand), err)
			tried++
			continue
		}
		return set, nil
	}
	if lastErr != nil {
		return zero, fmt.Errorf("%w (tried %d candidates; last error: %v)", ErrNotFound, tried, lastErr)
	}
	return zero, fmt.Errorf("%w (tried %d candidates)", ErrNotFound, tried)
}

func get(ctx context.Context, client *http.Client, rawURL string, limit int64, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", accept)
	resp, err := client.Do(req)
	if err != nil {
		return nil, redactError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("GET %s: %s", redactURL(rawURL), resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GET %s: body larger than %d bytes", redactURL(rawURL), limit)
	}
	return data, nil
}

// redactError strips the query from the URL http.Client embeds in transport
// errors, so a site token never reaches the manager log.
func redactError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.URL != "" {
		ue.URL = redactURL(ue.URL)
	}
	return err
}

// redactURL drops the query, userinfo, and fragment so error text and logs
// never carry a site's tokens.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid-url"
	}
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.User = nil
	return u.String()
}

var (
	linkTagRE = regexp.MustCompile("(?is)<link\\b([^>]*)>")
	attrRE    = regexp.MustCompile("(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\\s*=\\s*(?:\"([^\"]*)\"|'([^']*)'|([^\\s\"'>]+))")
)

type linkCandidate struct {
	url   string
	score int
}

// iconLinks returns absolute candidate URLs ordered by declared size, so a
// 256px apple-touch-icon wins over a 16px favicon.
func iconLinks(page []byte, base *url.URL) []string {
	var out []linkCandidate
	seen := make(map[string]bool)
	for _, m := range linkTagRE.FindAllSubmatch(page, -1) {
		attrs := parseAttrs(m[1])
		rel := strings.ToLower(attrs["rel"])
		if !isIconRel(rel) {
			continue
		}
		href := strings.TrimSpace(attrs["href"])
		if href == "" {
			continue
		}
		ref, err := url.Parse(href)
		if err != nil {
			continue
		}
		abs := base.ResolveReference(ref)
		if abs.Scheme != "http" && abs.Scheme != "https" {
			continue
		}
		key := abs.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, linkCandidate{url: key, score: iconScore(rel, attrs["sizes"])})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	urls := make([]string, len(out))
	for i, c := range out {
		urls[i] = c.url
	}
	return urls
}

func parseAttrs(raw []byte) map[string]string {
	attrs := make(map[string]string)
	for _, m := range attrRE.FindAllSubmatch(raw, -1) {
		name := strings.ToLower(string(m[1]))
		if _, ok := attrs[name]; ok {
			continue
		}
		value := string(m[2])
		if value == "" {
			value = string(m[3])
		}
		if value == "" {
			value = string(m[4])
		}
		attrs[name] = value
	}
	return attrs
}

func isIconRel(rel string) bool {
	for _, token := range strings.Fields(rel) {
		if token == "icon" || strings.HasPrefix(token, "apple-touch-icon") {
			return true
		}
	}
	return false
}

func iconScore(rel, sizes string) int {
	best := 0
	for _, field := range strings.Fields(sizes) {
		parts := strings.SplitN(strings.ToLower(field), "x", 2)
		if len(parts) != 2 {
			continue
		}
		var w, h int
		if _, err := fmt.Sscanf(parts[0], "%d", &w); err != nil {
			continue
		}
		if _, err := fmt.Sscanf(parts[1], "%d", &h); err != nil {
			continue
		}
		if w*h > best {
			best = w * h
		}
	}
	if best == 0 && strings.Contains(rel, "apple-touch-icon") {
		best = 180 * 180
	}
	return best
}
