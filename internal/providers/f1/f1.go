// Package f1 shows the Formula 1 season from Jolpica (the community-run
// successor to the Ergast API): the race calendar, or the driver or
// constructor standings. Public and keyless.
package f1

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

const userAgent = "Mozilla/5.0 (compatible; quarks/0.1; +https://github.com/maxbasque/quarks)"

type settings struct {
	Mode string `yaml:"mode"` // schedule (default) | drivers | constructors
	// Highlight marks a row in the standings: a driver code or surname
	// ("STR", "Stroll") or a team name ("Aston Martin"). Case-insensitive.
	Highlight string `yaml:"highlight"`
}

type Provider struct {
	mode      string
	highlight string
	base      string
	http      *http.Client
	now       func() time.Time
}

// New is the core.Factory for "f1".
func New(cfg core.WidgetConfig) (core.Provider, error) {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return nil, err
	}
	mode := strings.ToLower(strings.TrimSpace(s.Mode))
	switch mode {
	case "":
		mode = "schedule"
	case "schedule", "drivers", "constructors":
	default:
		return nil, fmt.Errorf("f1 widget %q: mode must be schedule, drivers or constructors", cfg.Title)
	}
	return &Provider{
		mode:      mode,
		highlight: strings.ToLower(strings.TrimSpace(s.Highlight)),
		base:      "https://api.jolpi.ca/ergast/f1/current",
		http:      &http.Client{Timeout: 20 * time.Second},
		now:       time.Now,
	}, nil
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	switch p.mode {
	case "drivers":
		return p.fetchDrivers(ctx)
	case "constructors":
		return p.fetchConstructors(ctx)
	default:
		return p.fetchSchedule(ctx)
	}
}

func (p *Provider) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+path, nil)
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

// ---- schedule --------------------------------------------------------------

type session struct {
	Date string `json:"date"`
	Time string `json:"time"`
}

// at is the session start; a missing time (some early-season entries) means
// the date alone, at midnight UTC.
func (s *session) at() time.Time {
	if s == nil || s.Date == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s.Date+"T"+firstNonEmpty(s.Time, "00:00:00Z"))
	if err != nil {
		return time.Time{}
	}
	return t
}

type race struct {
	Round    string `json:"round"`
	RaceName string `json:"raceName"`
	URL      string `json:"url"`
	Circuit  struct {
		CircuitName string `json:"circuitName"`
		Location    struct {
			Locality string `json:"locality"`
			Country  string `json:"country"`
		} `json:"Location"`
	} `json:"Circuit"`
	session
	Qualifying *session `json:"Qualifying"`
	Sprint     *session `json:"Sprint"`
}

type driver struct {
	Code       string `json:"code"`
	GivenName  string `json:"givenName"`
	FamilyName string `json:"familyName"`
}

type constructor struct {
	Name string `json:"name"`
}

// raceLength is how long after lights-out a race still counts as upcoming,
// so this weekend's race stays at the top until it's over.
const raceLength = 2 * time.Hour

// fetchSchedule lists the races still to run, soonest first, then the ones
// already run, most recent first, each with its winner when known.
func (p *Provider) fetchSchedule(ctx context.Context) (core.Payload, error) {
	var sched struct {
		MRData struct {
			RaceTable struct{ Races []race } `json:"RaceTable"`
		} `json:"MRData"`
	}
	if err := p.get(ctx, ".json", &sched); err != nil {
		return core.Payload{}, fmt.Errorf("f1 schedule: %w", err)
	}
	// winners are a nicety: the calendar still shows if this one fails
	winners := map[string]string{}
	var res struct {
		MRData struct {
			RaceTable struct {
				Races []struct {
					Round   string `json:"round"`
					Results []struct {
						Driver      driver      `json:"Driver"`
						Constructor constructor `json:"Constructor"`
					} `json:"Results"`
				}
			} `json:"RaceTable"`
		} `json:"MRData"`
	}
	if err := p.get(ctx, "/results/1.json?limit=100", &res); err == nil {
		for _, r := range res.MRData.RaceTable.Races {
			if len(r.Results) > 0 {
				w := r.Results[0]
				winners[r.Round] = w.Driver.GivenName + " " + w.Driver.FamilyName + " (" + w.Constructor.Name + ")"
			}
		}
	}

	now := p.now()
	var upcoming, past []core.Item
	for _, r := range sched.MRData.RaceTable.Races {
		start := r.at()
		it := core.Item{
			ID:          "f1-" + r.Round,
			Title:       r.RaceName,
			URL:         r.URL,
			Source:      "Round " + r.Round,
			PublishedAt: start,
		}
		if start.Add(raceLength).After(now) {
			type sess struct {
				name string
				at   time.Time
			}
			sessions := []sess{{"Qualifying", r.Qualifying.at()}, {"Sprint", r.Sprint.at()}, {"Race", start}}
			sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].at.Before(sessions[j].at) })
			var parts []string
			for _, ss := range sessions {
				if !ss.at.IsZero() {
					parts = append(parts, ss.name+" "+localTime(ss.at))
				}
			}
			it.Summary = r.Circuit.Location.Locality + ", " + r.Circuit.Location.Country + " · " + strings.Join(parts, " · ")
			upcoming = append(upcoming, it)
			continue
		}
		it.Summary = r.Circuit.Location.Locality + ", " + r.Circuit.Location.Country
		if w := winners[r.Round]; w != "" {
			it.Summary = "🏁 " + w + " · " + it.Summary
		}
		past = append(past, it)
	}
	sort.SliceStable(upcoming, func(i, j int) bool { return upcoming[i].PublishedAt.Before(upcoming[j].PublishedAt) })
	sort.SliceStable(past, func(i, j int) bool { return past[i].PublishedAt.After(past[j].PublishedAt) })
	return core.Feed(append(upcoming, past...)), nil
}

// localTime is a session start in the machine's time zone: "Sat Oct 3 22:00".
func localTime(t time.Time) string {
	return t.Local().Format("Mon Jan 2 15:04")
}

// ---- standings -------------------------------------------------------------

func (p *Provider) fetchDrivers(ctx context.Context) (core.Payload, error) {
	var data struct {
		MRData struct {
			StandingsTable struct {
				StandingsLists []struct {
					Round           string `json:"round"`
					DriverStandings []struct {
						Position     string        `json:"position"`
						Points       string        `json:"points"`
						Wins         string        `json:"wins"`
						Driver       driver        `json:"Driver"`
						Constructors []constructor `json:"Constructors"`
					} `json:"DriverStandings"`
				} `json:"StandingsLists"`
			} `json:"StandingsTable"`
		} `json:"MRData"`
	}
	if err := p.get(ctx, "/driverStandings.json", &data); err != nil {
		return core.Payload{}, fmt.Errorf("f1 driver standings: %w", err)
	}
	g := core.StandingsGroup{Columns: []string{"TEAM", "PTS", "W"}}
	if lists := data.MRData.StandingsTable.StandingsLists; len(lists) > 0 {
		g.Name = "After round " + lists[0].Round
		for i, d := range lists[0].DriverStandings {
			team := ""
			if len(d.Constructors) > 0 {
				team = d.Constructors[len(d.Constructors)-1].Name // a mid-season move lists the current team last
			}
			g.Rows = append(g.Rows, core.StandingsRow{
				Rank:      rank(d.Position, i),
				Team:      d.Driver.FamilyName,
				Abbrev:    d.Driver.Code,
				Values:    []string{team, d.Points, d.Wins},
				Highlight: p.matches(d.Driver.Code, d.Driver.FamilyName, team),
			})
		}
	}
	return core.Payload{Standings: &core.Standings{Groups: []core.StandingsGroup{g}}}, nil
}

func (p *Provider) fetchConstructors(ctx context.Context) (core.Payload, error) {
	var data struct {
		MRData struct {
			StandingsTable struct {
				StandingsLists []struct {
					Round                string `json:"round"`
					ConstructorStandings []struct {
						Position    string      `json:"position"`
						Points      string      `json:"points"`
						Wins        string      `json:"wins"`
						Constructor constructor `json:"Constructor"`
					} `json:"ConstructorStandings"`
				} `json:"StandingsLists"`
			} `json:"StandingsTable"`
		} `json:"MRData"`
	}
	if err := p.get(ctx, "/constructorStandings.json", &data); err != nil {
		return core.Payload{}, fmt.Errorf("f1 constructor standings: %w", err)
	}
	g := core.StandingsGroup{Columns: []string{"PTS", "W"}}
	if lists := data.MRData.StandingsTable.StandingsLists; len(lists) > 0 {
		g.Name = "After round " + lists[0].Round
		for i, c := range lists[0].ConstructorStandings {
			g.Rows = append(g.Rows, core.StandingsRow{
				Rank:      rank(c.Position, i),
				Team:      c.Constructor.Name,
				Values:    []string{c.Points, c.Wins},
				Highlight: p.matches(c.Constructor.Name),
			})
		}
	}
	return core.Payload{Standings: &core.Standings{Groups: []core.StandingsGroup{g}}}, nil
}

func (p *Provider) matches(names ...string) bool {
	if p.highlight == "" {
		return false
	}
	for _, n := range names {
		if strings.ToLower(n) == p.highlight {
			return true
		}
	}
	return false
}

// rank is the API's position, or the list order when it's missing (a
// driver who hasn't been classified yet).
func rank(pos string, i int) int {
	if n, err := strconv.Atoi(pos); err == nil {
		return n
	}
	return i + 1
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
