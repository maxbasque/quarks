// Package rss is the generic feed provider: RSS, Atom and JSON Feed via gofeed.
// Radio-Canada, YouTube per-channel feeds and the Reddit private home feed are
// all handled here — build this one well and most of M0-M2 follows (plan §11).
package rss

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/mmcdole/gofeed"
	ext "github.com/mmcdole/gofeed/extensions"

	"github.com/maxbasque/quarks/internal/core"
)

const userAgent = "quarks/0.1 (+https://github.com/maxbasque/quarks)"

type settings struct {
	Feeds  []string `yaml:"feeds"`
	Source string   `yaml:"source"` // display label override
	// interleave: with several feeds, round-robin their items (newest from each,
	// then next from each) instead of merging into one date-sorted list. Keeps a
	// busy feed from crowding out the others — the default for youtube.
	Interleave bool `yaml:"interleave"`
	// summary: show a short plain-text excerpt under each title (default true).
	Summary *bool `yaml:"summary"`
}

type Provider struct {
	feeds      []string
	source     string
	interleave bool
	summaries  bool // show a plain-text excerpt under each title
	parser     *gofeed.Parser
	http       *http.Client

	// per-feed conditional-request state. Fetch is never concurrent for one
	// widget (the scheduler serializes it), so no lock is needed.
	cond  map[string]condTags
	cache map[string][]core.Item
}

type condTags struct{ etag, lastMod string }

func newParser() *gofeed.Parser {
	p := gofeed.NewParser()
	p.UserAgent = userAgent
	return p
}

// New is the core.Factory for "rss".
func New(cfg core.WidgetConfig) (core.Provider, error) {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return nil, err
	}
	if len(s.Feeds) == 0 {
		return nil, fmt.Errorf("rss widget %q: no feeds", cfg.Title)
	}

	source := s.Source
	if source == "" {
		source = cfg.Title
	}
	summaries := s.Summary == nil || *s.Summary

	return newProvider(s.Feeds, source, s.Interleave, summaries), nil
}

// NewWithFeeds builds an rss provider directly, for other providers that are
// really just RSS with a nicer config surface (e.g. youtube).
func NewWithFeeds(feeds []string, source string, interleave, summaries bool) core.Provider {
	return newProvider(feeds, source, interleave, summaries)
}

func newProvider(feeds []string, source string, interleave, summaries bool) *Provider {
	return &Provider{
		feeds:      feeds,
		source:     source,
		interleave: interleave,
		summaries:  summaries,
		parser:     newParser(),
		http:       &http.Client{},
		cond:       map[string]condTags{},
		cache:      map[string][]core.Item{},
	}
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	var perFeed [][]core.Item
	var firstErr error

	for _, url := range p.feeds {
		one, err := p.feedItems(ctx, url)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", url, err)
			}
			continue
		}
		perFeed = append(perFeed, one)
	}

	if len(perFeed) == 0 {
		if firstErr != nil {
			return core.Payload{}, firstErr
		}
		return core.Feed(nil), nil
	}

	var items []core.Item
	if p.interleave && len(perFeed) > 1 {
		items = roundRobin(perFeed)
	} else {
		for _, f := range perFeed {
			items = append(items, f...)
		}
		sortByDate(items)
	}
	return core.Feed(items), nil
}

// feedItems fetches and parses one feed, using an ETag / Last-Modified
// conditional request. On a 304 it returns the previously parsed items without
// re-downloading or re-parsing.
func (p *Provider) feedItems(ctx context.Context, url string) ([]core.Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if c := p.cond[url]; c.etag != "" || c.lastMod != "" {
		if c.etag != "" {
			req.Header.Set("If-None-Match", c.etag)
		}
		if c.lastMod != "" {
			req.Header.Set("If-Modified-Since", c.lastMod)
		}
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return p.cache[url], nil // unchanged since last fetch
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}

	body := io.LimitReader(resp.Body, 16<<20)
	feed, err := p.parser.Parse(body)
	if err != nil {
		return nil, err
	}

	source := p.source
	if source == "" && feed.Title != "" {
		source = feed.Title
	}
	items := make([]core.Item, 0, len(feed.Items))
	for _, e := range feed.Items {
		items = append(items, p.toItem(e, source))
	}
	sortByDate(items)

	p.cond[url] = condTags{etag: resp.Header.Get("Etag"), lastMod: resp.Header.Get("Last-Modified")}
	p.cache[url] = items
	return items, nil
}

func sortByDate(items []core.Item) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
}

// roundRobin takes the 1st item of each feed, then the 2nd of each, and so on.
func roundRobin(feeds [][]core.Item) []core.Item {
	var out []core.Item
	for i := 0; ; i++ {
		done := true
		for _, f := range feeds {
			if i < len(f) {
				out = append(out, f[i])
				done = false
			}
		}
		if done {
			return out
		}
	}
}

func (p *Provider) toItem(e *gofeed.Item, source string) core.Item {
	it := core.Item{
		ID:     firstNonEmpty(e.GUID, e.Link),
		Title:  strings.TrimSpace(e.Title),
		URL:    e.Link,
		Source: source,
	}
	if e.PublishedParsed != nil {
		it.PublishedAt = *e.PublishedParsed
	} else if e.UpdatedParsed != nil {
		it.PublishedAt = *e.UpdatedParsed
	}
	if e.Author != nil {
		it.Author = e.Author.Name
	}
	if len(e.Authors) > 0 && it.Author == "" {
		it.Author = e.Authors[0].Name
	}
	if e.Image != nil {
		it.Thumbnail = e.Image.URL
	}
	if u := mediaThumbnail(e); u != "" {
		it.Thumbnail = u
	}
	// Some feeds (VGC, other WordPress) put the lead image in the description
	// HTML rather than a media tag.
	if it.Thumbnail == "" {
		if m := imgSrcRe.FindStringSubmatch(e.Description + e.Content); m != nil {
			it.Thumbnail = m[1]
		}
	}
	if p.summaries {
		it.Summary = summarize(firstNonEmpty(e.Description, e.Content))
	}

	// A Reddit home-feed RSS mixes many subreddits; surface which one from the
	// permalink (only reddit.com links match, so other feeds are untouched).
	if m := redditPathRe.FindStringSubmatch(it.URL); m != nil {
		it.Source = "r/" + m[1]
	}
	return it
}

var (
	redditPathRe = regexp.MustCompile(`(?:^|\.)reddit\.com/r/([A-Za-z0-9_]+)/`)
	imgSrcRe     = regexp.MustCompile(`<img[^>]+src=["']([^"']+)["']`)
	tagRe        = regexp.MustCompile(`<[^>]*>`)
	wsRe         = regexp.MustCompile(`\s+`)
	redditBoiler = regexp.MustCompile(`(?i)submitted by\s+/u/\S+\s+to\s+r/\S+`)
)

// summarize turns feed description/content HTML into a short plain-text excerpt.
func summarize(raw string) string {
	if raw == "" {
		return ""
	}
	txt := html.UnescapeString(tagRe.ReplaceAllString(raw, " "))
	txt = strings.TrimSpace(wsRe.ReplaceAllString(txt, " "))
	txt = strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(txt, "[link] [comments]")), "[link]")
	txt = strings.TrimSpace(txt)
	if redditBoiler.MatchString(txt) && len(txt) < 140 {
		return "" // Reddit link-post boilerplate, no real summary
	}
	const max = 260
	if len(txt) > max {
		if i := strings.LastIndex(txt[:max], " "); i > 0 {
			txt = txt[:i]
		} else {
			txt = txt[:max]
		}
		txt += "…"
	}
	return txt
}

// mediaThumbnail digs an image URL out of the Media RSS extension: a bare
// <media:thumbnail> or <media:content> (many news feeds), or the
// <media:group><media:thumbnail> nesting YouTube's per-channel feeds use.
func mediaThumbnail(e *gofeed.Item) string {
	media := e.Extensions["media"]
	if media == nil {
		return ""
	}
	if u := attrURL(media["thumbnail"]); u != "" {
		return u
	}
	if g := media["group"]; len(g) > 0 {
		if u := attrURL(g[0].Children["thumbnail"]); u != "" {
			return u
		}
		if u := attrURL(g[0].Children["content"]); u != "" {
			return u
		}
	}
	if u := attrURL(media["content"]); u != "" {
		return u
	}
	return ""
}

func attrURL(exts []ext.Extension) string {
	if len(exts) > 0 {
		return exts[0].Attrs["url"]
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
