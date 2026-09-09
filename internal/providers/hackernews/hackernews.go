// Package hackernews fetches Hacker News stories from the Algolia search API.
// One request returns points and comment counts, which the per-channel RSS feed
// (hnrss) does not carry — so this is the provider for HN, with the generic rss
// provider left as a fallback (plan §11).
package hackernews

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/maxbasque/quarks/internal/core"
)

const (
	endpoint  = "https://hn.algolia.com/api/v1/search"
	itemURL   = "https://news.ycombinator.com/item?id="
	userAgent = "quarks/0.1 (+https://github.com/maxbasque/quarks)"
)

type settings struct {
	Tags  string `yaml:"tags"`  // Algolia tag filter, default "front_page"
	Query string `yaml:"query"` // optional search term
}

type Provider struct {
	tags  string
	query string
	limit int
	http  *http.Client
}

// New is the core.Factory for "hackernews".
func New(cfg core.WidgetConfig) (core.Provider, error) {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return nil, err
	}
	if s.Tags == "" {
		s.Tags = "front_page"
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = 30
	}
	return &Provider{
		tags:  s.Tags,
		query: s.Query,
		limit: limit,
		http:  &http.Client{},
	}, nil
}

type apiResponse struct {
	Hits []struct {
		ObjectID    string `json:"objectID"`
		Title       string `json:"title"`
		URL         string `json:"url"`
		Author      string `json:"author"`
		Points      int    `json:"points"`
		NumComments int    `json:"num_comments"`
		CreatedAtI  int64  `json:"created_at_i"`
		StoryText   string `json:"story_text"`
	} `json:"hits"`
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	q := url.Values{}
	q.Set("tags", p.tags)
	q.Set("hitsPerPage", strconv.Itoa(p.limit))
	if p.query != "" {
		q.Set("query", p.query)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return core.Payload{}, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := p.http.Do(req)
	if err != nil {
		return core.Payload{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return core.Payload{}, fmt.Errorf("algolia: http %d", resp.StatusCode)
	}

	var data apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return core.Payload{}, err
	}

	items := make([]core.Item, 0, len(data.Hits))
	for _, h := range data.Hits {
		if h.Title == "" {
			continue
		}
		discussion := itemURL + h.ObjectID
		link := h.URL
		if link == "" {
			link = discussion // Ask HN / text posts
		}
		items = append(items, core.Item{
			ID:          h.ObjectID,
			Title:       h.Title,
			URL:         link,
			Source:      "Hacker News",
			Author:      h.Author,
			PublishedAt: time.Unix(h.CreatedAtI, 0).UTC(),
			Score:       h.Points,
			Comments:    h.NumComments,
			CommentsURL: discussion,
			Body:        h.StoryText,
		})
	}
	return core.Feed(items), nil
}
