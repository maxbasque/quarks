package spotify

import (
	"context"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maxbasque/quarks/internal/core"
	"github.com/maxbasque/quarks/internal/spotifyapi"
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

func TestParseSettingsRejectsInvertedThresholds(t *testing.T) {
	_, err := parseSettings(widgetConfig(t, "type: spotify\ntitle: X\ninclude: eps\n"+
		"ep_min_tracks: 8\nep_max_tracks: 3\n"+
		"client_id: cid\nclient_secret: cs\nrefresh_token: rt\n"))
	if err == nil {
		t.Fatal("expected an error when ep_min_tracks > ep_max_tracks")
	}
}

func TestParseSettingsDefaults(t *testing.T) {
	p, err := parseSettings(widgetConfig(t, "type: spotify\ntitle: X\n"+
		"client_id: cid\nclient_secret: cs\nrefresh_token: rt\n"))
	if err != nil {
		t.Fatalf("parseSettings: %v", err)
	}
	if p.include != "album" {
		t.Errorf("include = %q, want album (default)", p.include)
	}
	if p.minEP != 3 || p.maxEP != 7 {
		t.Errorf("minEP/maxEP = %d/%d, want 3/7 (defaults)", p.minEP, p.maxEP)
	}
}

func TestParseSettingsHonorsCustomThresholds(t *testing.T) {
	p, err := parseSettings(widgetConfig(t, "type: spotify\ntitle: X\ninclude: eps\n"+
		"ep_min_tracks: 4\nep_max_tracks: 9\n"+
		"client_id: cid\nclient_secret: cs\nrefresh_token: rt\n"))
	if err != nil {
		t.Fatalf("parseSettings: %v", err)
	}
	if p.minEP != 4 || p.maxEP != 9 {
		t.Errorf("minEP/maxEP = %d/%d, want 4/9", p.minEP, p.maxEP)
	}
}

// newProviderForTest builds a Provider around an isolated, network-free
// shared cache — bypassing New()/acquire() entirely, the same pattern
// shared_test.go uses via newTestShared().
func newProviderForTest(include string, minEP, maxEP int) *Provider {
	return &Provider{shared: newTestShared(), include: include, minEP: minEP, maxEP: maxEP}
}

func TestFetchMapsSnapshotToItems(t *testing.T) {
	p := newProviderForTest("album", 3, 7)
	date := time.Now().Add(48 * time.Hour)
	p.shared.releases["a1"] = releaseEntry{
		album: spotifyapi.Album{
			ID: "a1", Name: "New Album", AlbumType: "album", TotalTracks: 12,
			ArtistName: "Some Artist", URL: "https://open.spotify.com/album/a1", ImageURL: "https://img/a1.jpg",
		},
		date: date,
	}

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(got.Items))
	}
	it := got.Items[0]
	if it.Title != "New Album" || it.Source != "Some Artist" || it.URL != "https://open.spotify.com/album/a1" {
		t.Errorf("unexpected item: %+v", it)
	}
	if it.Thumbnail != "https://img/a1.jpg" {
		t.Errorf("Thumbnail = %q", it.Thumbnail)
	}
	if it.Summary != "Album · 12 tracks" {
		t.Errorf("Summary = %q", it.Summary)
	}
	if !it.PublishedAt.Equal(date) {
		t.Errorf("PublishedAt = %v, want %v", it.PublishedAt, date)
	}
}

func TestFetchOnlyReturnsMatchingInclude(t *testing.T) {
	p := newProviderForTest("album", 3, 7)
	date := time.Now().Add(24 * time.Hour)
	p.shared.releases["ep1"] = releaseEntry{
		album: spotifyapi.Album{ID: "ep1", Name: "An EP", AlbumType: "single", TotalTracks: 5},
		date:  date,
	}

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Items) != 0 {
		t.Fatalf("albums widget should not surface an EP-classified release, got %+v", got.Items)
	}
}

func TestFetchEmptyCacheIsNotAnError(t *testing.T) {
	p := newProviderForTest("album", 3, 7)
	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Items) != 0 {
		t.Errorf("expected no items, got %+v", got.Items)
	}
}

func TestSummarize(t *testing.T) {
	if got := summarize("album", 1); got != "Album · 1 track" {
		t.Errorf("summarize(album, 1) = %q", got)
	}
	if got := summarize("eps", 3); got != "EP · 3 tracks" {
		t.Errorf("summarize(eps, 3) = %q", got)
	}
}
