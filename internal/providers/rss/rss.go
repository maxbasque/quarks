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

func (p *Provider) Fetch(ctx context.Context) ([]core.Item, error) {
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
		return nil, firstErr
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
	return items, nil
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
	if mt, ok := e.Extensions["media"]["thumbnail"]; ok && len(mt) > 0 {
		if u := mt[0].Attrs["url"]; u != "" {
			it.Thumbnail = u
		}
	}
	return it
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
