// Package onthisday surfaces Wikipedia's "On this day" entries for the current
// date — historical events, births, deaths and holidays. It uses the keyless
// Wikimedia REST feed, which is available for a handful of languages (en, fr,
// de, sv, pt, ru, es, …).
package onthisday

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maxbasque/quarks/internal/core"
)

const userAgent = "quarks/0.1 (+https://github.com/maxbasque/quarks)"

// validTypes are the feed sub-types the REST endpoint accepts. "selected" is the
// curated highlight reel and the sensible default; "all" merges every bucket.
var validTypes = map[string]bool{
	"selected": true, "events": true, "births": true,
	"deaths": true, "holidays": true, "all": true,
}

type settings struct {
	Lang string `yaml:"lang"` // wiki language code, default "fr"
	Kind string `yaml:"mode"` // selected | events | births | deaths | holidays | all
}

type Provider struct {
	lang  string
	kind  string
	limit int
	base  string           // overridable for tests
	now   func() time.Time // overridable for tests
	http  *http.Client
}

// New is the core.Factory for "onthisday".
func New(cfg core.WidgetConfig) (core.Provider, error) {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return nil, err
	}
	lang := strings.ToLower(strings.TrimSpace(s.Lang))
	if lang == "" {
		lang = "fr"
	}
	kind := strings.ToLower(strings.TrimSpace(s.Kind))
	if kind == "" {
		kind = "selected"
	}
	if !validTypes[kind] {
		return nil, fmt.Errorf("onthisday widget %q: mode must be one of selected, events, births, deaths, holidays, all", cfg.Title)
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = 15
	}
	return &Provider{
		lang:  lang,
		kind:  kind,
		limit: limit,
		base:  "https://%s.wikipedia.org/api/rest_v1/feed/onthisday",
		now:   time.Now,
		http:  &http.Client{},
	}, nil
}

// apiResponse covers every sub-type: a single-type request fills one slice, the
// "all" request fills several.
type apiResponse struct {
	Selected []entry `json:"selected"`
	Events   []entry `json:"events"`
	Births   []entry `json:"births"`
	Deaths   []entry `json:"deaths"`
	Holidays []entry `json:"holidays"`
}

type entry struct {
	Text  string `json:"text"`
	Year  int    `json:"year"`
	Pages []struct {
		Title       string `json:"title"`
		Extract     string `json:"extract"`
		ContentURLs struct {
			Desktop struct {
				Page string `json:"page"`
			} `json:"desktop"`
		} `json:"content_urls"`
		Thumbnail struct {
			Source string `json:"source"`
		} `json:"thumbnail"`
	} `json:"pages"`
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	now := p.now()
	url := fmt.Sprintf(p.base, p.lang) + fmt.Sprintf("/%s/%02d/%02d", p.kind, int(now.Month()), now.Day())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return core.Payload{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return core.Payload{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return core.Payload{}, fmt.Errorf("wikimedia: http %d", resp.StatusCode)
	}

	var data apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return core.Payload{}, err
	}

	var entries []entry
	entries = append(entries, data.Selected...)
	entries = append(entries, data.Events...)
	entries = append(entries, data.Births...)
	entries = append(entries, data.Deaths...)
	entries = append(entries, data.Holidays...)

	// Most recent year first — reads like a feed rather than a timeline.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Year > entries[j].Year })

	items := make([]core.Item, 0, len(entries))
	for _, e := range entries {
		text := strings.TrimSpace(e.Text)
		if text == "" {
			continue
		}
		title := text
		if e.Year > 0 {
			title = fmt.Sprintf("%d — %s", e.Year, text)
		}
		it := core.Item{
			ID:     fmt.Sprintf("otd-%02d%02d-%d-%.60s", int(now.Month()), now.Day(), e.Year, text),
			Title:  title,
			Source: "Wikipédia",
			URL:    fmt.Sprintf("https://%s.wikipedia.org/wiki/Special:Search?search=%s", p.lang, text),
		}
		if len(e.Pages) > 0 {
			pg := e.Pages[0]
			if u := pg.ContentURLs.Desktop.Page; u != "" {
				it.URL = u
			} else if pg.Title != "" {
				it.URL = fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", p.lang, pg.Title)
			}
			it.Thumbnail = pg.Thumbnail.Source
			it.Summary = clip(pg.Extract, 220)
		}
		items = append(items, it)
	}

	if len(items) > p.limit {
		items = items[:p.limit]
	}
	return core.Feed(items), nil
}

var (
	wsRe  = regexp.MustCompile(`\s+`)
	tagRe = regexp.MustCompile(`<[^>]*>`)
)

// clip normalizes an extract to a short single-line plain-text excerpt. The REST
// feed's "extract" is already plain text, but strip tags defensively.
func clip(s string, max int) string {
	s = html.UnescapeString(tagRe.ReplaceAllString(s, ""))
	s = strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
	if len(s) <= max {
		return s
	}
	if i := strings.LastIndex(s[:max], " "); i > 0 {
		s = s[:i]
	} else {
		s = s[:max]
	}
	return s + "…"
}
