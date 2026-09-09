package standings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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

func TestNHL(t *testing.T) {
	body := `{"standings":[
	  {"teamAbbrev":{"default":"TOR"},"teamCommonName":{"default":"Maple Leafs"},"teamLogo":"tor.svg",
	   "divisionName":"Atlantic","gamesPlayed":82,"wins":50,"losses":24,"otLosses":8,"points":108,"divisionSequence":2,"streakCode":"W","streakCount":3},
	  {"teamAbbrev":{"default":"MTL"},"teamCommonName":{"default":"Canadiens"},"teamLogo":"mtl.svg",
	   "divisionName":"Atlantic","gamesPlayed":82,"wins":52,"losses":22,"otLosses":8,"points":112,"divisionSequence":1,"streakCode":"L","streakCount":1},
	  {"teamAbbrev":{"default":"NYR"},"teamCommonName":{"default":"Rangers"},"teamLogo":"nyr.svg",
	   "divisionName":"Metropolitan","gamesPlayed":82,"wins":45,"losses":30,"otLosses":7,"points":97,"divisionSequence":1}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	p, err := New(widgetConfig(t, "type: standings\ntitle: NHL\nleague: nhl\nteam: mtl\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.(*Provider).nhlURL = srv.URL

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	s := got.Standings
	if s == nil || len(s.Groups) != 2 {
		t.Fatalf("want 2 groups, got %+v", s)
	}
	atl := s.Groups[0]
	if atl.Name != "Atlantic" || len(atl.Rows) != 2 {
		t.Fatalf("Atlantic = %q, %d rows", atl.Name, len(atl.Rows))
	}
	// sorted by divisionSequence: MTL (1) then TOR (2)
	if atl.Rows[0].Abbrev != "MTL" || atl.Rows[0].Rank != 1 || !atl.Rows[0].Highlight {
		t.Errorf("row 0 = %+v", atl.Rows[0])
	}
	if atl.Rows[0].Values[1] != "52-22-8" || atl.Rows[0].Values[2] != "112" {
		t.Errorf("MTL values = %v", atl.Rows[0].Values)
	}
	if atl.Rows[1].Values[3] != "W3" {
		t.Errorf("TOR streak = %q", atl.Rows[1].Values[3])
	}
}

func TestMLB(t *testing.T) {
	body := `{"records":[
	  {"division":{"id":201},"teamRecords":[
	    {"team":{"id":147,"name":"Yankees"},"wins":95,"losses":67,"winningPercentage":".586","gamesBack":"-","divisionRank":"1","streak":{"streakCode":"W2"}},
	    {"team":{"id":111,"name":"Red Sox"},"wins":89,"losses":73,"winningPercentage":".549","gamesBack":"6.0","divisionRank":"2","streak":{"streakCode":"L1"}}
	  ]}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	p, _ := New(widgetConfig(t, "type: standings\ntitle: MLB\nleague: mlb\nteam: Yankees\n"))
	p.(*Provider).mlbURL = srv.URL

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	g := got.Standings.Groups[0]
	if g.Name != "AL East" {
		t.Errorf("group name = %q", g.Name)
	}
	if g.Rows[0].Team != "Yankees" || !g.Rows[0].Highlight {
		t.Errorf("row 0 = %+v", g.Rows[0])
	}
	if g.Rows[0].Values[0] != "95-67" || g.Rows[0].Values[1] != ".586" || g.Rows[0].Values[2] != "—" {
		t.Errorf("Yankees values = %v", g.Rows[0].Values)
	}
	if g.Rows[1].Values[2] != "6.0" {
		t.Errorf("Red Sox GB = %q", g.Rows[1].Values[2])
	}
}

func TestBadLeague(t *testing.T) {
	if _, err := New(widgetConfig(t, "type: standings\ntitle: X\nleague: nfl\n")); err == nil {
		t.Error("expected an error for an unknown league")
	}
}
