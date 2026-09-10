package ebird

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

const body = `[
  {"speciesCode":"buffle","comName":"Bufflehead","sciName":"Bucephala albeola",
   "locName":"Parc du Mont-Royal","obsDt":"2026-09-08 07:45","howMany":3,
   "subId":"S12345","userDisplayName":"A. Birder","subnational2Name":"Montréal","subnational1Name":"Québec"},
  {"speciesCode":"woodstork","comName":"Wood Stork","sciName":"Mycteria americana",
   "locName":"Île des Sœurs","obsDt":"2026-09-07","howMany":1,"subId":"S12346","subnational1Name":"Québec"},
  {"speciesCode":"blank","comName":""}
]`

func TestRegion(t *testing.T) {
	var gotPath, gotToken, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-eBirdApiToken")
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	p, err := New(widgetConfig(t, "type: ebird\ntitle: Oiseaux\nregion: ca-qc\ntoken: abc123\nlimit: 10\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.(*Provider).base = srv.URL

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotPath != "/data/obs/CA-QC/recent/notable" {
		t.Errorf("path = %q", gotPath)
	}
	if gotToken != "abc123" {
		t.Errorf("token header = %q", gotToken)
	}
	if !strings.Contains(gotQuery, "detail=full") || !strings.Contains(gotQuery, "maxResults=10") {
		t.Errorf("query = %q", gotQuery)
	}
	if len(got.Items) != 2 {
		t.Fatalf("got %d items, want 2 (blank name dropped)", len(got.Items))
	}

	first := got.Items[0]
	if first.Title != "Bufflehead (3)" {
		t.Errorf("Title = %q", first.Title)
	}
	if first.Source != "Parc du Mont-Royal" || first.Author != "A. Birder" {
		t.Errorf("Source/Author = %q / %q", first.Source, first.Author)
	}
	if first.URL != "https://ebird.org/checklist/S12345" {
		t.Errorf("URL = %q", first.URL)
	}
	if first.Summary != "Bucephala albeola · Montréal" {
		t.Errorf("Summary = %q", first.Summary)
	}
	if first.PublishedAt.IsZero() {
		t.Errorf("PublishedAt should parse from obsDt")
	}
	if got.Items[1].PublishedAt.IsZero() {
		t.Errorf("date-only obsDt should still parse")
	}
	if got.Items[1].Summary != "Mycteria americana · Québec" {
		t.Errorf("Summary[1] = %q", got.Items[1].Summary)
	}
}

func TestGeo(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	p, err := New(widgetConfig(t, "type: ebird\ntitle: Oiseaux\nlat: 45.50\nlng: -73.57\ndist: 30\ntoken: k\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.(*Provider).base = srv.URL

	if _, err := p.Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotPath != "/data/obs/geo/recent/notable" {
		t.Errorf("path = %q", gotPath)
	}
	for _, want := range []string{"lat=45.50", "lng=-73.57", "dist=30"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
}

func TestNoLocation(t *testing.T) {
	_, err := New(widgetConfig(t, "type: ebird\ntitle: x\ntoken: k\n"))
	if err == nil || !strings.Contains(err.Error(), "region") {
		t.Fatalf("want location error, got %v", err)
	}
}

func TestNoToken(t *testing.T) {
	p, err := New(widgetConfig(t, "type: ebird\ntitle: x\nregion: CA-QC\n"))
	if err != nil {
		t.Fatalf("New should succeed without a token: %v", err)
	}
	_, err = p.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "secrets.yaml") {
		t.Fatalf("want token error at Fetch, got %v", err)
	}
}
