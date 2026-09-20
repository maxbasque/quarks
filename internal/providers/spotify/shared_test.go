package spotify

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maxbasque/quarks/internal/spotifyapi"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name        string
		albumType   string
		totalTracks int
		want        string
	}{
		{"album type always wins", "album", 1, "album"},
		{"album type wins even with many tracks", "album", 20, "album"},
		{"1 track is a single", "single", 1, "single"},
		{"2 tracks is a single", "single", 2, "single"},
		{"3 tracks is an EP", "single", 3, "eps"},
		{"7 tracks is an EP", "single", 7, "eps"},
		{"8 tracks falls back to album", "single", 8, "album"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			al := spotifyapi.Album{AlbumType: c.albumType, TotalTracks: c.totalTracks}
			if got := classify(al, 3, 7); got != c.want {
				t.Errorf("classify(%s, %d tracks) = %q, want %q", c.albumType, c.totalTracks, got, c.want)
			}
		})
	}
}

func TestClassifyCustomThresholds(t *testing.T) {
	al := spotifyapi.Album{AlbumType: "single", TotalTracks: 4}
	if got := classify(al, 5, 9); got != "album" {
		t.Errorf("with a raised minEP, 4 tracks should fall back to album, got %q", got)
	}
}

func TestRotationSize(t *testing.T) {
	cases := []struct {
		total int
		want  int
	}{
		{0, 0},
		{1, 1},
		{288, 1},
		{289, 2},
		{500, 2},
		{576, 2},
		{577, 3},
	}
	for _, c := range cases {
		if got := rotationSize(c.total); got != c.want {
			t.Errorf("rotationSize(%d) = %d, want %d", c.total, got, c.want)
		}
	}
}

func TestParseReleaseDate(t *testing.T) {
	cases := []struct {
		date, precision string
		want            string // formatted 2006-01-02 if ok
		ok              bool
	}{
		{"2026-11-13", "day", "2026-11-13", true},
		{"2026-11", "month", "2026-11-01", true},
		{"2026", "year", "2026-01-01", true},
		{"not-a-date", "day", "", false},
	}
	for _, c := range cases {
		got, ok := parseReleaseDate(c.date, c.precision)
		if ok != c.ok {
			t.Fatalf("parseReleaseDate(%q,%q) ok=%v, want %v", c.date, c.precision, ok, c.ok)
		}
		if ok && got.Format("2006-01-02") != c.want {
			t.Errorf("parseReleaseDate(%q,%q) = %v, want %s", c.date, c.precision, got, c.want)
		}
	}
}

func newTestShared() *shared {
	return &shared{
		client:   spotifyapi.NewClient(),
		log:      slog.Default(),
		releases: map[string]releaseEntry{},
	}
}

func TestIngestKeepsOnlyUpcoming(t *testing.T) {
	sh := newTestShared()
	future := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	past := time.Now().AddDate(0, 0, -10).Format("2006-01-02")

	sh.ingest("Artist", []spotifyapi.Album{
		{ID: "upcoming", Name: "Upcoming", ReleaseDate: future, ReleaseDatePrecision: "day"},
		{ID: "past", Name: "Past", ReleaseDate: past, ReleaseDatePrecision: "day"},
		{ID: "bad-date", Name: "Bad", ReleaseDate: "garbage", ReleaseDatePrecision: "day"},
	})

	sh.mu.RLock()
	defer sh.mu.RUnlock()
	if len(sh.releases) != 1 {
		t.Fatalf("got %d cached releases, want 1: %+v", len(sh.releases), sh.releases)
	}
	if _, ok := sh.releases["upcoming"]; !ok {
		t.Error("upcoming release should be cached")
	}
}

func TestIngestFillsArtistNameWhenAlbumOmitsIt(t *testing.T) {
	sh := newTestShared()
	future := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	sh.ingest("Fallback Artist", []spotifyapi.Album{
		{ID: "a1", Name: "X", ReleaseDate: future, ReleaseDatePrecision: "day"},
	})
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	if sh.releases["a1"].album.ArtistName != "Fallback Artist" {
		t.Errorf("ArtistName = %q", sh.releases["a1"].album.ArtistName)
	}
}

func TestIngestThenReIngestPastDateRemovesIt(t *testing.T) {
	sh := newTestShared()
	future := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	sh.ingest("Artist", []spotifyapi.Album{{ID: "a1", Name: "X", ReleaseDate: future, ReleaseDatePrecision: "day"}})

	past := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	sh.ingest("Artist", []spotifyapi.Album{{ID: "a1", Name: "X", ReleaseDate: past, ReleaseDatePrecision: "day"}})

	sh.mu.RLock()
	defer sh.mu.RUnlock()
	if _, ok := sh.releases["a1"]; ok {
		t.Error("a release that's no longer upcoming should be dropped on re-ingest")
	}
}

func TestPruneDropsPastReleasesRegardlessOfWhichArtist(t *testing.T) {
	sh := newTestShared()
	// Simulate a release that was upcoming when cached but whose date has
	// since passed, without going through ingest (which would filter it) —
	// this is exactly what prune exists to clean up on other artists' ticks.
	sh.releases["a1"] = releaseEntry{
		album: spotifyapi.Album{ID: "a1", Name: "Old"},
		date:  time.Now().Add(-time.Hour),
	}
	sh.releases["a2"] = releaseEntry{
		album: spotifyapi.Album{ID: "a2", Name: "Future"},
		date:  time.Now().Add(24 * time.Hour),
	}

	sh.prune()

	sh.mu.RLock()
	defer sh.mu.RUnlock()
	if _, ok := sh.releases["a1"]; ok {
		t.Error("past release should have been pruned")
	}
	if _, ok := sh.releases["a2"]; !ok {
		t.Error("future release should survive prune")
	}
}

func TestSnapshotFiltersByClassAndSortsByDate(t *testing.T) {
	sh := newTestShared()
	now := time.Now()
	sh.releases["album-1"] = releaseEntry{
		album: spotifyapi.Album{ID: "album-1", Name: "Later Album", AlbumType: "album", TotalTracks: 10},
		date:  now.Add(48 * time.Hour),
	}
	sh.releases["ep-1"] = releaseEntry{
		album: spotifyapi.Album{ID: "ep-1", Name: "Soon EP", AlbumType: "single", TotalTracks: 5},
		date:  now.Add(24 * time.Hour),
	}
	sh.releases["single-1"] = releaseEntry{
		album: spotifyapi.Album{ID: "single-1", Name: "A Single", AlbumType: "single", TotalTracks: 1},
		date:  now.Add(12 * time.Hour),
	}

	albums := sh.snapshot("album", 3, 7)
	if len(albums) != 1 || albums[0].album.ID != "album-1" {
		t.Errorf("album snapshot = %+v", albums)
	}

	eps := sh.snapshot("eps", 3, 7)
	if len(eps) != 1 || eps[0].album.ID != "ep-1" {
		t.Errorf("eps snapshot = %+v", eps)
	}

	singles := sh.snapshot("single", 3, 7)
	if len(singles) != 1 || singles[0].album.ID != "single-1" {
		t.Errorf("singles should still be classifiable even though no widget requests them: %+v", singles)
	}
}

func TestSnapshotSortsSoonestFirst(t *testing.T) {
	sh := newTestShared()
	now := time.Now()
	sh.releases["later"] = releaseEntry{
		album: spotifyapi.Album{ID: "later", AlbumType: "album"},
		date:  now.Add(72 * time.Hour),
	}
	sh.releases["sooner"] = releaseEntry{
		album: spotifyapi.Album{ID: "sooner", AlbumType: "album"},
		date:  now.Add(6 * time.Hour),
	}

	out := sh.snapshot("album", 3, 7)
	if len(out) != 2 || out[0].album.ID != "sooner" || out[1].album.ID != "later" {
		t.Errorf("expected sooner-first order, got %+v", out)
	}
}

// Deliberately not testing acquire() itself here: it starts a real background
// goroutine (run) that would hit the live Spotify API, which has no place in
// an offline unit test. cacheKey is the actual thing two Factory calls need
// to agree on to converge on the same *shared, so that's what's under test.
func TestCacheKeyDedupesByRefreshToken(t *testing.T) {
	if cacheKey("same-token") != cacheKey("same-token") {
		t.Error("identical refresh tokens should produce the same cache key")
	}
	if cacheKey("token-a") == cacheKey("token-b") {
		t.Error("different refresh tokens should produce different cache keys")
	}
}

// pollableTestShared builds a *shared wired to a fixture ArtistAlbums server,
// with the followed-artist list and access token already warm — so
// maybePoll's own pollSlice call only ever needs to hit the fixture's
// /artists/.../albums route, isolating the test to maybePoll's gating logic.
func pollableTestShared(t *testing.T, artistAlbumsHits *atomic.Int32) *shared {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		artistAlbumsHits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{}, "next": nil})
	}))
	t.Cleanup(srv.Close)

	client := spotifyapi.NewClient()
	client.APIBase = srv.URL
	return &shared{
		client:           client,
		log:              slog.Default(),
		releases:         map[string]releaseEntry{},
		artists:          []spotifyapi.Artist{{ID: "a1", Name: "Artist One"}},
		artistsCheckedAt: time.Now(), // skip ensureArtists' network call
		tok:              "at-1",     // skip accessToken's refresh call
		tokExp:           time.Now().Add(time.Hour),
	}
}

func TestMaybePollActuallyPolls(t *testing.T) {
	var hits atomic.Int32
	sh := pollableTestShared(t, &hits)

	sh.maybePoll(context.Background())

	if hits.Load() != 1 {
		t.Errorf("expected 1 ArtistAlbums call, got %d", hits.Load())
	}
	if sh.lastPollAt.IsZero() {
		t.Error("lastPollAt should be set after a poll")
	}
}

func TestMaybePollSkipsWithinInterval(t *testing.T) {
	var hits atomic.Int32
	sh := pollableTestShared(t, &hits)

	sh.maybePoll(context.Background())
	sh.maybePoll(context.Background()) // immediately again — same tab or the other tab, or a double refresh click

	if hits.Load() != 1 {
		t.Errorf("expected the second call within pollInterval to be a no-op, got %d total calls", hits.Load())
	}
}

func TestMaybePollPollsAgainAfterIntervalElapses(t *testing.T) {
	var hits atomic.Int32
	sh := pollableTestShared(t, &hits)

	sh.maybePoll(context.Background())
	sh.lastPollAt = time.Now().Add(-pollInterval - time.Second) // simulate time passing
	sh.maybePoll(context.Background())

	if hits.Load() != 2 {
		t.Errorf("expected a second poll once pollInterval elapsed, got %d calls", hits.Load())
	}
}
