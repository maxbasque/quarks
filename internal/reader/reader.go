// Package reader extracts the main text of an article for inline reading. It is
// invoked lazily when the user opens an item — never during background polling
// (plan §11).
package reader

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	readability "github.com/go-shiori/go-readability"
	"github.com/microcosm-cc/bluemonday"
)

const (
	cacheTTL    = time.Hour
	cacheMax    = 64
	maxBodySize = 8 << 20 // 8 MiB
	fetchUA     = "Mozilla/5.0 (compatible; quarks/0.1; +https://github.com/maxbasque/quarks)"
)

// Article is the sanitized, readable form of a page.
type Article struct {
	Title    string
	Byline   string
	SiteName string
	URL      string
	HTML     template.HTML // sanitized
	Excerpt  string
}

type Reader struct {
	http   *http.Client
	policy *bluemonday.Policy

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	art Article
	at  time.Time
}

func New() *Reader {
	policy := bluemonday.UGCPolicy()
	policy.RequireNoFollowOnLinks(true)
	policy.AllowAttrs("class").Globally()

	return &Reader{
		http:   &http.Client{Timeout: 20 * time.Second},
		policy: policy,
		cache:  make(map[string]cached),
	}
}

// Get fetches and extracts rawURL, using a short-lived in-memory cache.
func (r *Reader) Get(ctx context.Context, rawURL string) (Article, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return Article{}, fmt.Errorf("reader: unsupported URL %q", rawURL)
	}

	if art, ok := r.fromCache(rawURL); ok {
		return art, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Article{}, err
	}
	req.Header.Set("User-Agent", fetchUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := r.http.Do(req)
	if err != nil {
		return Article{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Article{}, fmt.Errorf("reader: %s returned %d", rawURL, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "html") {
		return Article{}, fmt.Errorf("reader: %s is not an HTML page (%s)", rawURL, ct)
	}

	parsed, err := readability.FromReader(io.LimitReader(resp.Body, maxBodySize), u)
	if err != nil {
		return Article{}, fmt.Errorf("reader: could not extract %s: %w", rawURL, err)
	}

	art := Article{
		Title:    strings.TrimSpace(parsed.Title),
		Byline:   strings.TrimSpace(parsed.Byline),
		SiteName: strings.TrimSpace(parsed.SiteName),
		URL:      rawURL,
		HTML:     template.HTML(r.policy.Sanitize(parsed.Content)), //nolint:gosec // sanitized above
		Excerpt:  strings.TrimSpace(parsed.Excerpt),
	}
	r.store(rawURL, art)
	return art, nil
}

func (r *Reader) fromCache(key string) (Article, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.cache[key]; ok && time.Since(e.at) < cacheTTL {
		return e.art, true
	}
	return Article{}, false
}

func (r *Reader) store(key string, art Article) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cache) >= cacheMax {
		r.cache = make(map[string]cached) // crude eviction; articles are cheap to refetch
	}
	r.cache[key] = cached{art: art, at: time.Now()}
}
