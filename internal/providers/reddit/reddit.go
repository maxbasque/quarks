// Package reddit fetches public subreddit listings from reddit.com/r/<subs>.json.
//
// This is the *fragile* Reddit path — it carries scores and comment counts, but
// the .json endpoint is frequently blocked outside a residential IP. For your
// personalized home feed use a private RSS feed URL through the generic rss
// provider instead (plan §9); that path is not rate-limited and needs no code.
package reddit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/maxbasque/quarks/internal/core"
)

const (
	userAgent   = "quarks/0.1 (+https://github.com/maxbasque/quarks)"
	defaultBase = "https://www.reddit.com"
)

type settings struct {
	Subreddits []string `yaml:"subreddits"`
	Sort       string   `yaml:"sort"` // hot | new | top | rising
}

type Provider struct {
	base  string // scheme+host, overridable in tests
	path  string // /r/<subs>/<sort>.json
	limit int
	http  *http.Client
}

// New is the core.Factory for "reddit".
func New(cfg core.WidgetConfig) (core.Provider, error) {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return nil, err
	}
	if len(s.Subreddits) == 0 {
		return nil, fmt.Errorf("reddit widget %q: no subreddits", cfg.Title)
	}
	sort := s.Sort
	if sort == "" {
		sort = "hot"
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = 25
	}
	return &Provider{
		base:  defaultBase,
		path:  fmt.Sprintf("/r/%s/%s.json", strings.Join(s.Subreddits, "+"), sort),
		limit: limit,
		http:  &http.Client{},
	}, nil
}

type listing struct {
	Data struct {
		Children []struct {
			Data struct {
				ID                    string  `json:"id"`
				Title                 string  `json:"title"`
				URL                   string  `json:"url"`
				Permalink             string  `json:"permalink"`
				Score                 int     `json:"score"`
				NumComments           int     `json:"num_comments"`
				CreatedUTC            float64 `json:"created_utc"`
				Author                string  `json:"author"`
				SubredditNamePrefixed string  `json:"subreddit_name_prefixed"`
				Thumbnail             string  `json:"thumbnail"`
				IsSelf                bool    `json:"is_self"`
				Stickied              bool    `json:"stickied"`
			} `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	url := fmt.Sprintf("%s%s?limit=%d&raw_json=1", p.base, p.path, p.limit)
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

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK || !looksLikeJSON(resp.Header.Get("Content-Type"), body) {
		return core.Payload{}, fmt.Errorf(
			"reddit returned %d and not JSON — the public .json endpoint is often blocked "+
				"outside a residential IP; use a private RSS feed instead (plan §9)", resp.StatusCode)
	}

	var l listing
	if err := json.Unmarshal(body, &l); err != nil {
		return core.Payload{}, err
	}

	items := make([]core.Item, 0, len(l.Data.Children))
	for _, c := range l.Data.Children {
		d := c.Data
		if d.Stickied || d.Title == "" {
			continue
		}
		permalink := "https://www.reddit.com" + d.Permalink
		link := d.URL
		if d.IsSelf || link == "" {
			link = permalink
		}
		it := core.Item{
			ID:          d.ID,
			Title:       d.Title,
			URL:         link,
			Source:      d.SubredditNamePrefixed,
			Author:      "u/" + d.Author,
			PublishedAt: time.Unix(int64(d.CreatedUTC), 0).UTC(),
			Score:       d.Score,
			Comments:    d.NumComments,
			CommentsURL: permalink,
		}
		if strings.HasPrefix(d.Thumbnail, "http") {
			it.Thumbnail = d.Thumbnail
		}
		items = append(items, it)
	}
	return core.Feed(items), nil
}

func looksLikeJSON(contentType string, body []byte) bool {
	if strings.Contains(contentType, "json") {
		return true
	}
	b := strings.TrimSpace(string(body))
	return strings.HasPrefix(b, "{") || strings.HasPrefix(b, "[")
}
