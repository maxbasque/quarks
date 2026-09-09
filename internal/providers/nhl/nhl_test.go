package nhl

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

func TestFetch(t *testing.T) {
	soon := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-10 * 24 * time.Hour).UTC().Format(time.RFC3339)

	body := `{"games":[
	  {"id":1,"startTimeUTC":"` + past + `","gameState":"OFF","gameType":2,
	   "awayTeam":{"abbrev":"MTL","commonName":{"default":"Canadiens"},"score":3,"logo":"mtl.svg"},
	   "homeTeam":{"abbrev":"TOR","commonName":{"default":"Maple Leafs"},"score":2,"logo":"tor.svg"}},
	  {"id":2,"startTimeUTC":"` + soon + `","gameState":"FUT","gameType":1,
	   "venue":{"default":"Bell Centre"},
	   "awayTeam":{"abbrev":"OTT","commonName":{"default":"Senators"},"logo":"ott.svg"},
	   "homeTeam":{"abbrev":"MTL","commonName":{"default":"Canadiens"},"logo":"mtl.svg"}}
	]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/MTL/") {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	p, err := New(widgetConfig(t, "type: nhl\ntitle: Habs\nteam: mtl\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.(*Provider).endpoint = srv.URL + "/v1/club-schedule-season/%s/now"

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// the 10-day-old game is dropped; the upcoming one remains
	if len(got.Items) != 1 {
		t.Fatalf("want 1 item, got %d: %+v", len(got.Items), got.Items)
	}
	it := got.Items[0]
	if it.Title != "vs Senators" {
		t.Errorf("title = %q (MTL home vs OTT)", it.Title)
	}
	if !strings.Contains(it.Summary, "Bell Centre") || !strings.Contains(it.Summary, ":") {
		t.Errorf("summary should carry the date + venue, got %q", it.Summary)
	}
	if it.Source != "Preseason" {
		t.Errorf("source = %q", it.Source)
	}
	if it.Thumbnail != "ott.svg" {
		t.Errorf("thumbnail should be the opponent logo, got %q", it.Thumbnail)
	}
	if it.URL != "https://www.nhl.com/gamecenter/2" {
		t.Errorf("url = %q", it.URL)
	}
}

func TestRequiresTeam(t *testing.T) {
	if _, err := New(widgetConfig(t, "type: nhl\ntitle: X\n")); err == nil {
		t.Error("expected an error without team")
	}
}
