// Package rss is the generic feed provider: RSS, Atom and JSON Feed via gofeed.
// Radio-Canada, YouTube per-channel feeds and the Reddit private home feed are
// all handled here — build this one well and most of M0-M2 follows (plan §11).
package rss

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mmcdole/gofeed"

	"github.com/maxbasque/quarks/internal/core"
)

type settings struct {
	Feeds  []string `yaml:"feeds"`
	Source string   `yaml:"source"` // display label override
}

type Provider struct {
	feeds  []string
	source string
	parser *gofeed.Parser
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

	p := gofeed.NewParser()
	p.UserAgent = "quarks/0.1 (+https://github.com/maxbasque/quarks)"

	source := s.Source
	if source == "" {
		source = cfg.Title
	}

	return &Provider{feeds: s.Feeds, source: source, parser: p}, nil
}

// NewWithFeeds builds an rss provider directly, for other providers that are
// really just RSS with a nicer config surface (e.g. youtube).
func NewWithFeeds(feeds []string, source string) core.Provider {
	p := gofeed.NewParser()
	p.UserAgent = "quarks/0.1 (+https://github.com/maxbasque/quarks)"
	return &Provider{feeds: feeds, source: source, parser: p}
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	var items []core.Item
	var firstErr error

	for _, url := range p.feeds {
		feed, err := p.parser.ParseURLWithContext(url, ctx)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", url, err)
			}
			continue
		}
		source := p.source
		if source == "" && feed.Title != "" {
			source = feed.Title
		}
		for _, e := range feed.Items {
			items = append(items, toItem(e, source))
		}
	}

	// If every feed failed, surface the error. A partial success is returned.
	if items == nil && firstErr != nil {
		return core.Payload{}, firstErr
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
	return core.Feed(items), nil
}

func toItem(e *gofeed.Item, source string) core.Item {
	it := core.Item{
		ID:     firstNonEmpty(e.GUID, e.Link),
		Title:  strings.TrimSpace(e.Title),
		URL:    e.Link,
		Source: source,
		Body:   "", // reader view fills this lazily
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
	return it
}

// mediaThumbnail digs a thumbnail URL out of the Media RSS extension, covering
// both a bare <media:thumbnail> and the <media:group><media:thumbnail> nesting
// that YouTube's per-channel feeds use.
func mediaThumbnail(e *gofeed.Item) string {
	media := e.Extensions["media"]
	if media == nil {
		return ""
	}
	if t := media["thumbnail"]; len(t) > 0 {
		if u := t[0].Attrs["url"]; u != "" {
			return u
		}
	}
	if g := media["group"]; len(g) > 0 {
		if t := g[0].Children["thumbnail"]; len(t) > 0 {
			if u := t[0].Attrs["url"]; u != "" {
				return u
			}
		}
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
