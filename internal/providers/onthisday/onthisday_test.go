package onthisday

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
	const body = `{"selected":[
	  {"text":"Premier événement","year":1901,"pages":[
	    {"title":"Truc","extract":"Une <b>explication</b>.","content_urls":{"desktop":{"page":"https://fr.wikipedia.org/wiki/Truc"}},"thumbnail":{"source":"https://img/truc.jpg"}}]},
	  {"text":"Événement récent","year":1999,"pages":[]},
	  {"text":"Sans année","year":0,"pages":[]}
	]}`

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	p, err := New(widgetConfig(t, "type: onthisday\ntitle: Éphéméride\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pv := p.(*Provider)
	pv.base = srv.URL + "/%s"
	pv.now = func() time.Time { return time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC) }

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotPath != "/fr/selected/03/07" {
		t.Errorf("request path = %q, want /fr/selected/03/07", gotPath)
	}
	if len(got.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(got.Items))
	}
	// Most recent year first.
	if got.Items[0].Title != "1999 — Événement récent" {
		t.Errorf("item[0].Title = %q", got.Items[0].Title)
	}
	if got.Items[2].Title != "Sans année" {
		t.Errorf("item[2].Title = %q, want unprefixed", got.Items[2].Title)
	}
	withPage := got.Items[1]
	if withPage.URL != "https://fr.wikipedia.org/wiki/Truc" || withPage.Thumbnail != "https://img/truc.jpg" {
		t.Errorf("page fields not mapped: %+v", withPage)
	}
	if withPage.Summary != "Une explication." {
		t.Errorf("summary = %q, want tags stripped", withPage.Summary)
	}
	if !got.Items[0].PublishedAt.IsZero() {
		t.Errorf("PublishedAt should stay zero for historical entries")
	}
	if got.Items[0].Score != 0 {
		t.Errorf("Score should be 0, got %d", got.Items[0].Score)
	}
}

func TestModeValidation(t *testing.T) {
	if _, err := New(widgetConfig(t, "type: onthisday\ntitle: x\n")); err != nil {
		t.Fatalf("default mode should be accepted: %v", err)
	}
	if _, err := New(widgetConfig(t, "type: onthisday\ntitle: x\nmode: births\n")); err != nil {
		t.Fatalf("mode births should be accepted: %v", err)
	}
	_, err := New(widgetConfig(t, "type: onthisday\ntitle: x\nmode: bogus\n"))
	if err == nil || !strings.Contains(err.Error(), "mode must be") {
		t.Fatalf("want mode validation error, got %v", err)
	}
}

func TestModeInPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"births":[{"text":"X","year":1950,"pages":[]}]}`))
	}))
	defer srv.Close()

	p, _ := New(widgetConfig(t, "type: onthisday\ntitle: x\nmode: births\nlang: en\n"))
	pv := p.(*Provider)
	pv.base = srv.URL + "/%s"
	pv.now = func() time.Time { return time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC) }

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotPath != "/en/births/12/01" {
		t.Errorf("path = %q", gotPath)
	}
	if len(got.Items) != 1 || got.Items[0].Title != "1950 — X" {
		t.Errorf("items = %+v", got.Items)
	}
}
