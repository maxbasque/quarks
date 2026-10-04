package spotify

import (
	"context"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maxbasque/quarks/internal/core"
	"github.com/maxbasque/quarks/internal/fr"
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

// parseSettings is pure (no network), so these exercise it directly rather
// than through New(), which — via acquire() — starts a real background
// poller against the live Spotify API. That's fine for production but has no
// place in an offline unit test.

func TestParseSettingsToleratesMissingCredentials(t *testing.T) {
	// Missing credentials is a transient "not connected yet" state, not a
	// config authoring mistake — see the comment on Provider in spotify.go.
	// It must not be a hard parse error, or pasting the Media snippet before
	// finishing Connect would fail the entire config load, not just this tab.
	p, err := parseSettings(widgetConfig(t, "type: spotify\ntitle: Albums\ninclude: album\n"))
	if err != nil {
		t.Fatalf("parseSettings: %v", err)
	}
	if p.creds.ClientID != "" || p.creds.ClientSecret != "" || p.creds.RefreshToken != "" {
		t.Errorf("expected empty credentials, got %+v", p.creds)
	}
}

func TestNewWithoutCredentialsBuildsButFetchErrors(t *testing.T) {
	prov, err := New(widgetConfig(t, "type: spotify\ntitle: Albums\ninclude: album\n"))
	if err != nil {
		t.Fatalf("New should succeed even without credentials, got: %v", err)
	}
	if _, err := prov.Fetch(context.Background()); err == nil {
		t.Fatal("Fetch should report not-connected as a per-widget error")
	}
}

func TestParseSettingsRejectsBadInclude(t *testing.T) {
	_, err := parseSettings(widgetConfig(t, "type: spotify\ntitle: X\ninclude: singles\n"+
		"client_id: cid\nclient_secret: cs\nrefresh_token: rt\n"))
	if err == nil {
		t.Fatal("expected an error for an unsupported include value")
	}
}

func TestParseSettingsDefaults(t *testing.T) {
	p, err := parseSettings(widgetConfig(t, "type: spotify\ntitle: X\n"+
		"client_id: cid\nclient_secret: cs\nrefresh_token: rt\n"))
	if err != nil {
		t.Fatalf("parseSettings: %v", err)
	}
	if p.include != "all" {
		t.Errorf("include = %q, want all (default)", p.include)
	}
}

func TestParseSettingsIgnoresRetiredTrackThresholds(t *testing.T) {
	// Configs written for the Spotify-only version still carry these.
	_, err := parseSettings(widgetConfig(t, "type: spotify\ntitle: X\ninclude: eps\n"+
		"ep_min_tracks: 8\nep_max_tracks: 3\n"))
	if err != nil {
		t.Fatalf("old ep_*_tracks keys must not break the config: %v", err)
	}
}

func newProviderForTest(include string) *Provider {
	return &Provider{shared: newTestShared(), include: include}
}

func TestFetchMapsSnapshotToItems(t *testing.T) {
	p := newProviderForTest("album")
	day := time.Now().AddDate(0, 0, 3)
	e := entry("rg1", "album", day.Format("2006-01-02"))
	e.rg.Title, e.rg.ArtistCredit, e.rg.SecondaryTypes = "New Album", "Some Artist", []string{"Live"}
	p.shared.releases["rg1"] = e

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(got.Items))
	}
	it := got.Items[0]
	if it.ID != "rg1" || it.Title != "New Album" || it.Source != "Some Artist" || it.URL != "https://musicbrainz.org/release-group/rg1" {
		t.Errorf("unexpected item: %+v", it)
	}
	if want := "Album · Live · " + fr.DayMonthYear(day); it.Summary != want {
		t.Errorf("Summary = %q, want %q", it.Summary, want)
	}
	if !it.PublishedAt.Equal(e.date) {
		t.Errorf("PublishedAt = %v, want %v", it.PublishedAt, e.date)
	}
}

func TestFetchImpreciseDateHasNoCountdown(t *testing.T) {
	p := newProviderForTest("eps")
	month := time.Now().AddDate(0, 1, 0)
	p.shared.releases["rg1"] = entry("rg1", "eps", month.Format("2006-01"))

	got, _ := p.Fetch(context.Background())
	if len(got.Items) != 1 {
		t.Fatalf("got %d items", len(got.Items))
	}
	if !got.Items[0].PublishedAt.IsZero() {
		t.Error("a month-only date must not render as a precise countdown")
	}
	if want := "EP · " + fr.MonthYear(month); got.Items[0].Summary != want {
		t.Errorf("Summary = %q, want %q", got.Items[0].Summary, want)
	}
}

func TestFetchOnlyReturnsMatchingInclude(t *testing.T) {
	p := newProviderForTest("album")
	p.shared.releases["ep1"] = entry("ep1", "eps", time.Now().AddDate(0, 0, 1).Format("2006-01-02"))
	got, err := p.Fetch(context.Background())
	if err != nil || len(got.Items) != 0 {
		t.Fatalf("albums widget should not surface an EP: %+v, %v", got.Items, err)
	}
}

func TestFetchAllMixesAlbumsAndEPs(t *testing.T) {
	p := newProviderForTest("all")
	p.shared.releases["ep1"] = entry("ep1", "eps", time.Now().AddDate(0, 0, 1).Format("2006-01-02"))
	p.shared.releases["al1"] = entry("al1", "album", time.Now().AddDate(0, 0, 2).Format("2006-01-02"))
	got, err := p.Fetch(context.Background())
	if err != nil || len(got.Items) != 2 || got.Items[0].ID != "ep1" || got.Items[1].ID != "al1" {
		t.Fatalf("want both, soonest first: %+v, %v", got.Items, err)
	}
	if !strings.HasPrefix(got.Items[0].Summary, "EP · ") || !strings.HasPrefix(got.Items[1].Summary, "Album · ") {
		t.Errorf("summaries should say which is which: %q, %q", got.Items[0].Summary, got.Items[1].Summary)
	}
}

func TestFetchEmptyCacheIsNotAnError(t *testing.T) {
	got, err := newProviderForTest("album").Fetch(context.Background())
	if err != nil || len(got.Items) != 0 {
		t.Fatalf("got %+v, %v", got.Items, err)
	}
}

func TestSummarizeFutureYear(t *testing.T) {
	if got := summarize(entry("x", "album", "2031")); got != "Album · 2031, date à venir" {
		t.Errorf("summarize = %q", got)
	}
}
