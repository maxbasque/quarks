package f1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maxbasque/quarks/internal/core"
)

func widgetConfig(t *testing.T, src string) core.WidgetConfig {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	wc, err := core.ParseWidget(*doc.Content[0])
	if err != nil {
		t.Fatalf("ParseWidget: %v", err)
	}
	return wc
}

func fixture(t *testing.T, mode string, routes map[string]string) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	p, err := New(widgetConfig(t, "type: f1\ntitle: F1\nmode: "+mode+"\nhighlight: str\n"))
	if err != nil {
		t.Fatal(err)
	}
	pr := p.(*Provider)
	pr.base = srv.URL + "/f1/current"
	pr.now = func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }
	return pr
}

const scheduleJSON = `{"MRData":{"RaceTable":{"Races":[
 {"round":"17","raceName":"Azerbaijan Grand Prix","url":"https://w/aze","date":"2026-09-20","time":"11:00:00Z",
  "Circuit":{"circuitName":"Baku","Location":{"locality":"Baku","country":"Azerbaijan"}}},
 {"round":"18","raceName":"Singapore Grand Prix","url":"https://w/sgp","date":"2026-10-04","time":"11:00:00Z",
  "Circuit":{"circuitName":"Marina Bay","Location":{"locality":"Marina Bay","country":"Singapore"}}},
 {"round":"19","raceName":"United States Grand Prix","url":"https://w/usa","date":"2026-10-18","time":"19:00:00Z",
  "Circuit":{"circuitName":"COTA","Location":{"locality":"Austin","country":"USA"}},
  "Qualifying":{"date":"2026-10-17","time":"21:00:00Z"},"Sprint":{"date":"2026-10-17","time":"17:00:00Z"}},
 {"round":"20","raceName":"Mexico City Grand Prix","url":"https://w/mex","date":"2026-10-25","time":"20:00:00Z",
  "Circuit":{"circuitName":"Hermanos Rodríguez","Location":{"locality":"Mexico City","country":"Mexico"}}}
]}}}`

const resultsJSON = `{"MRData":{"RaceTable":{"Races":[
 {"round":"17","Results":[{"Driver":{"givenName":"George","familyName":"Russell"},"Constructor":{"name":"Mercedes"}}]}
]}}}`

func TestSchedule(t *testing.T) {
	p := fixture(t, "schedule", map[string]string{
		"/f1/current.json":           scheduleJSON,
		"/f1/current/results/1.json": resultsJSON,
	})
	pl, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range pl.Items {
		got = append(got, it.Source)
	}
	// Singapore started an hour ago, so it's still "upcoming"; then the rest
	// of the season soonest first; then past races, most recent first
	if want := "Round 18,Round 19,Round 20,Round 17"; strings.Join(got, ",") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
	usa := pl.Items[1].Summary
	// sessions in running order: the sprint (17:00Z) comes before qualifying (21:00Z)
	sp, q, r := strings.Index(usa, "Sprint "), strings.Index(usa, "Qualifying "), strings.Index(usa, "Race ")
	if !strings.HasPrefix(usa, "Austin, USA · ") || sp < 0 || !(sp < q && q < r) {
		t.Errorf("USA summary = %q", usa)
	}
	if aze := pl.Items[3].Summary; !strings.HasPrefix(aze, "🏁 George Russell (Mercedes)") {
		t.Errorf("past race summary = %q", aze)
	}
}

func TestScheduleWithoutResults(t *testing.T) {
	p := fixture(t, "schedule", map[string]string{"/f1/current.json": scheduleJSON})
	pl, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("results failing should not fail the calendar: %v", err)
	}
	if len(pl.Items) != 4 || strings.Contains(pl.Items[3].Summary, "🏁") {
		t.Errorf("items = %+v", pl.Items)
	}
}

func TestDriverStandings(t *testing.T) {
	p := fixture(t, "drivers", map[string]string{"/f1/current/driverStandings.json": `{"MRData":{"StandingsTable":{"StandingsLists":[{"round":"16","DriverStandings":[
	 {"position":"1","points":"302","wins":"8","Driver":{"code":"ANT","givenName":"Andrea Kimi","familyName":"Antonelli"},"Constructors":[{"name":"Mercedes"}]},
	 {"position":"2","points":"280","wins":"5","Driver":{"code":"STR","givenName":"Lance","familyName":"Stroll"},"Constructors":[{"name":"Williams"},{"name":"Aston Martin"}]}
	]}]}}}`})
	pl, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	g := pl.Standings.Groups[0]
	if g.Name != "After round 16" || len(g.Rows) != 2 {
		t.Fatalf("group = %+v", g)
	}
	r := g.Rows[1]
	if r.Rank != 2 || r.Team != "Stroll" || r.Values[0] != "Aston Martin" || r.Values[1] != "280" || !r.Highlight {
		t.Errorf("row = %+v", r)
	}
	if g.Rows[0].Highlight {
		t.Errorf("unexpected highlight on %+v", g.Rows[0])
	}
}

func TestConstructorStandings(t *testing.T) {
	p := fixture(t, "constructors", map[string]string{"/f1/current/constructorStandings.json": `{"MRData":{"StandingsTable":{"StandingsLists":[{"round":"16","ConstructorStandings":[
	 {"position":"1","points":"538","wins":"11","Constructor":{"name":"Mercedes"}}
	]}]}}}`})
	pl, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r := pl.Standings.Groups[0].Rows[0]; r.Team != "Mercedes" || r.Values[0] != "538" || r.Values[1] != "11" {
		t.Errorf("row = %+v", r)
	}
}

func TestBadMode(t *testing.T) {
	if _, err := New(widgetConfig(t, "type: f1\ntitle: F1\nmode: qualifying\n")); err == nil {
		t.Error("want an error for an unknown mode")
	}
}
