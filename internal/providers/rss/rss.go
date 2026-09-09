// Package rss is the generic feed provider: RSS, Atom and JSON Feed via gofeed.
// Radio-Canada, YouTube per-channel feeds and the Reddit private home feed are
// all handled here — build this one well and most of M0-M2 follows (plan §11).
package rss

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mmcdole/gofeed"
	ext "github.com/mmcdole/gofeed/extensions"

	"github.com/maxbasque/quarks/internal/core"
)

type settings struct {
	Feeds  []string `yaml:"feeds"`
	Source string   `yaml:"source"` // display label override
	// interleave: with several feeds, round-robin their items (newest from each,
	// then next from each) instead of merging into one date-sorted list. Keeps a
	// busy feed from crowding out the others — the default for youtube.
	Interleave bool `yaml:"interleave"`
}

type Provider struct {
	feeds      []string
	source     string
	interleave bool
	parser     *gofeed.Parser
}

func newParser() *gofeed.Parser {
	p := gofeed.NewParser()
	p.UserAgent = "quarks/0.1 (+https://github.com/maxbasque/quarks)"
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

	return &Provider{feeds: s.Feeds, source: source, interleave: s.Interleave, parser: newParser()}, nil
}

// NewWithFeeds builds an rss provider directly, for other providers that are
// really just RSS with a nicer config surface (e.g. youtube).
func NewWithFeeds(feeds []string, source string, interleave bool) core.Provider {
	return &Provider{feeds: feeds, source: source, interleave: interleave, parser: newParser()}
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	var perFeed [][]core.Item
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
		one := make([]core.Item, 0, len(feed.Items))
		for _, e := range feed.Items {
			one = append(one, toItem(e, source))
		}
		sortByDate(one)
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
	// A Reddit home-feed RSS mixes many subreddits; surface which one from the
	// permalink (only reddit.com links match, so other feeds are untouched).
	if m := redditPathRe.FindStringSubmatch(it.URL); m != nil {
		it.Source = "r/" + m[1]
	}
	return it
}

var redditPathRe = regexp.MustCompile(`(?:^|\.)reddit\.com/r/([A-Za-z0-9_]+)/`)

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
