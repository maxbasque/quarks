// Package nhl turns an NHL team's season schedule into feed items — one per
// game, soonest first — using the public api-web.nhle.com endpoints (no key).
package nhl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/maxbasque/quarks/internal/core"
)

const (
	defaultEndpoint = "https://api-web.nhle.com/v1/club-schedule-season/%s/now"
	gameCenterURL   = "https://www.nhl.com/gamecenter/"
	userAgent       = "Mozilla/5.0 (compatible; quarks/0.1; +https://github.com/maxbasque/quarks)"
)

type settings struct {
	Team string `yaml:"team"` // 3-letter abbrev, e.g. MTL
}

type Provider struct {
	team     string
	limit    int
	endpoint string
	http     *http.Client
}

// New is the core.Factory for "nhl".
func New(cfg core.WidgetConfig) (core.Provider, error) {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return nil, err
	}
	s.Team = strings.ToUpper(strings.TrimSpace(s.Team))
	if len(s.Team) != 3 {
		return nil, fmt.Errorf("nhl widget %q: set team to a 3-letter code, e.g. MTL", cfg.Title)
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = 10
	}
	return &Provider{team: s.Team, limit: limit, endpoint: defaultEndpoint, http: &http.Client{}}, nil
}

type team struct {
	Abbrev     string         `json:"abbrev"`
	CommonName map[string]any `json:"commonName"`
	Score      *int           `json:"score"`
	Logo       string         `json:"logo"`
}

func (t team) name() string {
	if v, ok := t.CommonName["default"].(string); ok && v != "" {
		return v
	}
	return t.Abbrev
}

type game struct {
	ID           int64  `json:"id"`
	StartTimeUTC string `json:"startTimeUTC"`
	GameState    string `json:"gameState"`
	GameType     int    `json:"gameType"`
	Venue        struct {
		Default string `json:"default"`
	} `json:"venue"`
	Away team `json:"awayTeam"`
	Home team `json:"homeTeam"`
}

type response struct {
	Games []game `json:"games"`
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	url := fmt.Sprintf(p.endpoint, p.team)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		return core.Payload{}, fmt.Errorf("nhl api: http %d", resp.StatusCode)
	}

	var data response
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return core.Payload{}, err
	}

	cutoff := time.Now().Add(-30 * time.Hour) // keep a game visible through the night after
	var items []core.Item
	for _, g := range data.Games {
		start, err := time.Parse(time.RFC3339, g.StartTimeUTC)
		if err != nil || start.Before(cutoff) {
			continue
		}
		items = append(items, p.toItem(g, start))
		if len(items) >= p.limit {
			break
		}
	}
	return core.Feed(items), nil // already soonest-first from the API
}

func (p *Provider) toItem(g game, start time.Time) core.Item {
	us, them := g.Home, g.Away
	home := true
	if g.Away.Abbrev == p.team {
		us, them = g.Away, g.Home
		home = false
	}

	title := "vs " + them.name()
	if !home {
		title = "@ " + them.name()
	}

	it := core.Item{
		ID:          strconv.FormatInt(g.ID, 10),
		Title:       title,
		URL:         gameCenterURL + strconv.FormatInt(g.ID, 10),
		Source:      gameTypeLabel(g.GameType),
		PublishedAt: start,
		Thumbnail:   them.Logo,
		Summary:     gameSummary(g, us, them),
	}
	return it
}

func gameTypeLabel(t int) string {
	switch t {
	case 1:
		return "Preseason"
	case 3:
		return "Playoffs"
	default:
		return ""
	}
}

func gameSummary(g game, us, them team) string {
	live := g.GameState == "LIVE" || g.GameState == "CRIT"
	done := g.GameState == "OFF" || g.GameState == "FINAL"

	if (live || done) && us.Score != nil && them.Score != nil {
		result := fmt.Sprintf("%d–%d", *us.Score, *them.Score)
		switch {
		case live:
			return "Live · " + result
		case *us.Score > *them.Score:
			return "Won " + result
		case *us.Score < *them.Score:
			return "Lost " + result
		default:
			return "Final " + result
		}
	}
	if g.Venue.Default != "" {
		return g.Venue.Default
	}
	return ""
}
