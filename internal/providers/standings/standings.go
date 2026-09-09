// Package standings shows live league standings — NHL or MLB — as grouped
// tables. Both APIs are public and keyless.
package standings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/maxbasque/quarks/internal/core"
)

const userAgent = "Mozilla/5.0 (compatible; quarks/0.1; +https://github.com/maxbasque/quarks)"

type settings struct {
	League string `yaml:"league"` // nhl | mlb
	Group  string `yaml:"group"`  // division | conference | league   (nhl only)
	Team   string `yaml:"team"`   // optional: highlight this team's row
}

type Provider struct {
	league    string
	group     string
	highlight string
	nhlURL    string
	mlbURL    string
	http      *http.Client
}

// New is the core.Factory for "standings".
func New(cfg core.WidgetConfig) (core.Provider, error) {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return nil, err
	}
	league := strings.ToLower(strings.TrimSpace(s.League))
	if league != "nhl" && league != "mlb" {
		return nil, fmt.Errorf("standings widget %q: league must be nhl or mlb", cfg.Title)
	}
	group := strings.ToLower(strings.TrimSpace(s.Group))
	if group == "" {
		group = "division"
	}
	return &Provider{
		league:    league,
		group:     group,
		highlight: strings.ToUpper(strings.TrimSpace(s.Team)),
		nhlURL:    "https://api-web.nhle.com/v1/standings/now",
		mlbURL:    "https://statsapi.mlb.com/api/v1/standings?leagueId=103,104&standingsTypes=regularSeason",
		http:      &http.Client{},
	}, nil
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	if p.league == "mlb" {
		return p.fetchMLB(ctx)
	}
	return p.fetchNHL(ctx)
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
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// ---- NHL -----------------------------------------------------------------

func (p *Provider) fetchNHL(ctx context.Context) (core.Payload, error) {
	var data struct {
		Standings []struct {
			TeamAbbrev     struct{ Default string } `json:"teamAbbrev"`
			TeamCommonName struct{ Default string } `json:"teamCommonName"`
			TeamLogo       string                   `json:"teamLogo"`
			ConferenceName string                   `json:"conferenceName"`
			DivisionName   string                   `json:"divisionName"`
			GamesPlayed    int                      `json:"gamesPlayed"`
			Wins           int                      `json:"wins"`
			Losses         int                      `json:"losses"`
			OtLosses       int                      `json:"otLosses"`
			Points         int                      `json:"points"`
			DivisionSeq    int                      `json:"divisionSequence"`
			ConferenceSeq  int                      `json:"conferenceSequence"`
			LeagueSeq      int                      `json:"leagueSequence"`
			StreakCode     string                   `json:"streakCode"`
			StreakCount    int                      `json:"streakCount"`
		} `json:"standings"`
	}
	if err := p.get(ctx, p.nhlURL, &data); err != nil {
		return core.Payload{}, fmt.Errorf("nhl standings: %w", err)
	}

	type row struct {
		seq int
		r   core.StandingsRow
	}
	groups := map[string][]row{}
	var order []string

	for _, t := range data.Standings {
		var name string
		var seq int
		switch p.group {
		case "conference":
			name, seq = t.ConferenceName, t.ConferenceSeq
		case "league":
			name, seq = "", t.LeagueSeq
		default:
			name, seq = t.DivisionName, t.DivisionSeq
		}
		if _, ok := groups[name]; !ok {
			order = append(order, name)
		}
		streak := ""
		if t.StreakCode != "" && t.StreakCount > 0 {
			streak = t.StreakCode + strconv.Itoa(t.StreakCount)
		}
		groups[name] = append(groups[name], row{seq, core.StandingsRow{
			Team:      t.TeamCommonName.Default,
			Abbrev:    t.TeamAbbrev.Default,
			Logo:      t.TeamLogo,
			Values:    []string{strconv.Itoa(t.GamesPlayed), fmt.Sprintf("%d-%d-%d", t.Wins, t.Losses, t.OtLosses), strconv.Itoa(t.Points), streak},
			Highlight: t.TeamAbbrev.Default == p.highlight,
		}})
	}

	sort.Strings(order)
	st := &core.Standings{}
	for _, name := range order {
		rs := groups[name]
		sort.Slice(rs, func(i, j int) bool { return rs[i].seq < rs[j].seq })
		g := core.StandingsGroup{Name: name, Columns: []string{"GP", "W-L-OT", "PTS", "STRK"}}
		for i, r := range rs {
			r.r.Rank = i + 1
			g.Rows = append(g.Rows, r.r)
		}
		st.Groups = append(st.Groups, g)
	}
	return core.Payload{Standings: st}, nil
}

// ---- MLB -----------------------------------------------------------------

var mlbDivisions = map[int]struct {
	name  string
	order int
}{
	201: {"AL East", 0}, 202: {"AL Central", 1}, 200: {"AL West", 2},
	204: {"NL East", 3}, 205: {"NL Central", 4}, 203: {"NL West", 5},
}

func (p *Provider) fetchMLB(ctx context.Context) (core.Payload, error) {
	var data struct {
		Records []struct {
			Division    struct{ ID int } `json:"division"`
			TeamRecords []struct {
				Team struct {
					ID   int
					Name string
				} `json:"team"`
				Wins              int                         `json:"wins"`
				Losses            int                         `json:"losses"`
				WinningPercentage string                      `json:"winningPercentage"`
				GamesBack         string                      `json:"gamesBack"`
				DivisionRank      string                      `json:"divisionRank"`
				Streak            struct{ StreakCode string } `json:"streak"`
			} `json:"teamRecords"`
		} `json:"records"`
	}
	if err := p.get(ctx, p.mlbURL, &data); err != nil {
		return core.Payload{}, fmt.Errorf("mlb standings: %w", err)
	}

	sort.Slice(data.Records, func(i, j int) bool {
		return mlbDivisions[data.Records[i].Division.ID].order < mlbDivisions[data.Records[j].Division.ID].order
	})

	st := &core.Standings{}
	for _, rec := range data.Records {
		g := core.StandingsGroup{Name: mlbDivisions[rec.Division.ID].name, Columns: []string{"W-L", "PCT", "GB", "STRK"}}
		rows := rec.TeamRecords
		sort.Slice(rows, func(i, j int) bool { return atoi(rows[i].DivisionRank) < atoi(rows[j].DivisionRank) })
		for i, t := range rows {
			gb := t.GamesBack
			if gb == "-" {
				gb = "—"
			}
			g.Rows = append(g.Rows, core.StandingsRow{
				Rank:      i + 1,
				Team:      t.Team.Name,
				Abbrev:    t.Team.Name,
				Logo:      fmt.Sprintf("https://www.mlbstatic.com/team-logos/%d.svg", t.Team.ID),
				Values:    []string{fmt.Sprintf("%d-%d", t.Wins, t.Losses), strings.TrimPrefix(t.WinningPercentage, "0"), gb, t.Streak.StreakCode},
				Highlight: strings.EqualFold(t.Team.Name, p.highlight),
			})
		}
		st.Groups = append(st.Groups, g)
	}
	return core.Payload{Standings: st}, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
