// Package nhl talks to the public, keyless api-web.nhle.com. Two modes:
//
//	mode: schedule (default) — one team's upcoming games, soonest first
//	mode: scores             — recent final scores from around the league
package nhl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maxbasque/quarks/internal/core"
)

const (
	scheduleURL   = "https://api-web.nhle.com/v1/club-schedule-season/%s/now"
	scoreURL      = "https://api-web.nhle.com/v1/score/%s"
	gameCenterURL = "https://www.nhl.com/gamecenter/"
	userAgent     = "Mozilla/5.0 (compatible; quarks/0.1; +https://github.com/maxbasque/quarks)"
)

type settings struct {
	Team string `yaml:"team"` // 3-letter abbrev, e.g. MTL
	Mode string `yaml:"mode"` // schedule | scores
}

type Provider struct {
	mode        string
	team        string
	limit       int
	scheduleURL string
	scoreURL    string
	http        *http.Client
}

// New is the core.Factory for "nhl".
func New(cfg core.WidgetConfig) (core.Provider, error) {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return nil, err
	}
	mode := strings.ToLower(strings.TrimSpace(s.Mode))
	if mode == "" {
		mode = "schedule"
	}
	if mode != "schedule" && mode != "scores" {
		return nil, fmt.Errorf("nhl widget %q: mode must be schedule or scores", cfg.Title)
	}
	team := strings.ToUpper(strings.TrimSpace(s.Team))
	if mode == "schedule" && len(team) != 3 {
		return nil, fmt.Errorf("nhl widget %q: set team to a 3-letter code, e.g. MTL", cfg.Title)
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = 12
	}
	return &Provider{
		mode: mode, team: team, limit: limit,
		scheduleURL: scheduleURL, scoreURL: scoreURL, http: &http.Client{},
	}, nil
}

func (p *Provider) get(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("nhl api: http %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

type team struct {
	Abbrev     string         `json:"abbrev"`
	CommonName map[string]any `json:"commonName"`
	Name       map[string]any `json:"name"`
	Score      *int           `json:"score"`
	Logo       string         `json:"logo"`
}

func (t team) name() string {
	for _, m := range []map[string]any{t.CommonName, t.Name} {
		if v, ok := m["default"].(string); ok && v != "" {
			return v
		}
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
	GameOutcome struct {
		LastPeriodType string `json:"lastPeriodType"`
	} `json:"gameOutcome"`
	Away team `json:"awayTeam"`
	Home team `json:"homeTeam"`
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	if p.mode == "scores" {
		return p.fetchScores(ctx)
	}
	return p.fetchSchedule(ctx)
}

// ---- schedule ----------------------------------------------------------

func (p *Provider) fetchSchedule(ctx context.Context) (core.Payload, error) {
	var data struct {
		ClubTimezone string `json:"clubTimezone"`
		Games        []game `json:"games"`
	}
	if err := p.get(ctx, fmt.Sprintf(p.scheduleURL, p.team), &data); err != nil {
		return core.Payload{}, err
	}

	loc := time.UTC
	if l, err := time.LoadLocation(data.ClubTimezone); err == nil {
		loc = l
	}

	cutoff := time.Now().Add(-30 * time.Hour)
	var items []core.Item
	for _, g := range data.Games {
		start, err := time.Parse(time.RFC3339, g.StartTimeUTC)
		if err != nil || start.Before(cutoff) {
			continue
		}
		items = append(items, p.scheduleItem(g, start, loc))
		if len(items) >= p.limit {
			break
		}
	}
	return core.Feed(items), nil
}

func (p *Provider) scheduleItem(g game, start time.Time, loc *time.Location) core.Item {
	us, them := g.Home, g.Away
	title := "vs " + them.name()
	if g.Away.Abbrev == p.team {
		us, them = g.Away, g.Home
		title = "@ " + them.name()
	}

	summary := start.In(loc).Format("Mon Jan 2, 3:04 PM")
	if extra := gameExtra(g, us, them); extra != "" {
		summary += "  ·  " + extra
	}

	return core.Item{
		ID:          strconv.FormatInt(g.ID, 10),
		Title:       title,
		URL:         gameCenterURL + strconv.FormatInt(g.ID, 10),
		Source:      gameTypeLabel(g.GameType),
		PublishedAt: start,
		Thumbnail:   them.Logo,
		Summary:     summary,
	}
}

// gameExtra is the score once a game is under way, otherwise the venue.
func gameExtra(g game, us, them team) string {
	live := g.GameState == "LIVE" || g.GameState == "CRIT"
	done := g.GameState == "OFF" || g.GameState == "FINAL"
	if (live || done) && us.Score != nil && them.Score != nil {
		r := fmt.Sprintf("%d–%d", *us.Score, *them.Score)
		switch {
		case live:
			return "Live " + r
		case *us.Score > *them.Score:
			return "Won " + r
		case *us.Score < *them.Score:
			return "Lost " + r
		default:
			return "Final " + r
		}
	}
	return g.Venue.Default
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

// ---- scores (league-wide) -------------------------------------------

func (p *Provider) fetchScores(ctx context.Context) (core.Payload, error) {
	date := "now"
	seen := map[int64]bool{}
	var items []core.Item

	for hop := 0; hop < 5 && len(items) < p.limit; hop++ {
		var d struct {
			PrevDate string `json:"prevDate"`
			Games    []game `json:"games"`
		}
		if err := p.get(ctx, fmt.Sprintf(p.scoreURL, date), &d); err != nil {
			if hop == 0 {
				return core.Payload{}, err
			}
			break
		}
		for _, g := range d.Games {
			if seen[g.ID] || g.Away.Score == nil || g.Home.Score == nil {
				continue
			}
			switch g.GameState {
			case "OFF", "FINAL", "LIVE", "CRIT":
				seen[g.ID] = true
				items = append(items, scoreItem(g))
			}
		}
		if d.PrevDate == "" || d.PrevDate == date {
			break
		}
		date = d.PrevDate
	}

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
	if len(items) > p.limit {
		items = items[:p.limit]
	}
	return core.Feed(items), nil
}

func scoreItem(g game) core.Item {
	a, h := g.Away, g.Home
	as, hs := *a.Score, *h.Score

	winner := h
	if as > hs {
		winner = a
	}

	state := "Final"
	switch {
	case g.GameState == "LIVE" || g.GameState == "CRIT":
		state = "Live"
	case g.GameOutcome.LastPeriodType == "OT":
		state = "Final (OT)"
	case g.GameOutcome.LastPeriodType == "SO":
		state = "Final (SO)"
	}

	start, _ := time.Parse(time.RFC3339, g.StartTimeUTC)
	summary := state
	if !start.IsZero() {
		summary += "  ·  " + start.Local().Format("Mon Jan 2")
	}

	return core.Item{
		ID:          strconv.FormatInt(g.ID, 10),
		Title:       fmt.Sprintf("%s %d – %d %s", a.Abbrev, as, hs, h.Abbrev),
		URL:         gameCenterURL + strconv.FormatInt(g.ID, 10),
		PublishedAt: start,
		Thumbnail:   winner.Logo,
		Summary:     summary,
	}
}
