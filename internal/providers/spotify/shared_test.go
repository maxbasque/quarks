package spotify

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maxbasque/quarks/internal/musicbrainz"
	"github.com/maxbasque/quarks/internal/spotifyapi"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		primary   string
		secondary []string
		want      string
	}{
		{"Album", nil, "album"},
		{"Album", []string{"Live"}, "album"},
		{"Album", []string{"Soundtrack"}, "album"},
		{"EP", nil, "eps"},
		{"Single", nil, ""},
		{"Broadcast", nil, ""},
		{"", nil, ""},
		{"Album", []string{"Compilation"}, ""},
		{"EP", []string{"Remix"}, ""},
		{"Album", []string{"DJ-mix"}, ""},
	}
	for _, c := range cases {
		rg := musicbrainz.ReleaseGroup{PrimaryType: c.primary, SecondaryTypes: c.secondary}
		if got := classify(rg); got != c.want {
			t.Errorf("classify(%s %v) = %q, want %q", c.primary, c.secondary, got, c.want)
		}
	}
}

func TestParseReleaseDate(t *testing.T) {
	cases := []struct {
		in, want, precision string
		ok                  bool
	}{
		{"2026-11-13", "2026-11-13", "day", true},
		{"2026-11", "2026-11-01", "month", true},
		{"2026", "2026-01-01", "year", true},
		{"", "", "", false},
		{"garbage", "", "", false},
	}
	for _, c := range cases {
		got, precision, ok := parseReleaseDate(c.in)
		if ok != c.ok || precision != c.precision {
			t.Fatalf("parseReleaseDate(%q) = _, %q, %v", c.in, precision, ok)
		}
		if ok && got.Format("2006-01-02") != c.want {
			t.Errorf("parseReleaseDate(%q) = %v, want %s", c.in, got, c.want)
		}
	}
}

func TestUpcoming(t *testing.T) {
	now := time.Date(2026, 9, 26, 15, 0, 0, 0, time.Local)
	d := func(s string) (time.Time, string) {
		t, p, _ := parseReleaseDate(s)
		return t, p
	}
	cases := []struct {
		date string
		want bool
	}{
		{"2026-09-27", true},
		{"2026-09-26", true}, // out today: still shown until the day is over
		{"2026-09-25", false},
		{"2026-09", true}, // "sometime this month"
		{"2026-10", true},
		{"2026-08", false},
		{"2026", false}, // a bare current year is usually an undated catalog entry
		{"2027", true},
	}
	for _, c := range cases {
		start, p := d(c.date)
		if got := upcoming(start, p, now); got != c.want {
			t.Errorf("upcoming(%s) = %v, want %v", c.date, got, c.want)
		}
	}
}

func TestCacheKeyDedupesByRefreshToken(t *testing.T) {
	if cacheKey("same-token") != cacheKey("same-token") {
		t.Error("identical refresh tokens should produce the same cache key")
	}
	if cacheKey("token-a") == cacheKey("token-b") {
		t.Error("different refresh tokens should produce different cache keys")
	}
}

// newTestShared returns a *shared that never touches the network: lastPollAt
// is "just now", so maybePoll is a no-op for pollInterval.
func newTestShared() *shared {
	sh := newShared(spotifyapi.NewClient(), musicbrainz.NewClient(), slog.Default())
	sh.lastPollAt = wallNow()
	return sh
}

func entry(id, class, date string) releaseEntry {
	t, p, _ := parseReleaseDate(date)
	return releaseEntry{rg: musicbrainz.ReleaseGroup{ID: id, Title: id, ArtistCredit: "Artist " + id}, date: t, precision: p, class: class}
}

func TestSnapshotFiltersByClassAndSortsSoonestFirst(t *testing.T) {
	sh := newTestShared()
	later := time.Now().AddDate(0, 0, 20).Format("2006-01-02")
	sooner := time.Now().AddDate(0, 0, 5).Format("2006-01-02")
	sh.releases["later"] = entry("later", "album", later)
	sh.releases["sooner"] = entry("sooner", "album", sooner)
	sh.releases["ep"] = entry("ep", "eps", sooner)

	albums := sh.snapshot("album")
	if len(albums) != 2 || albums[0].rg.ID != "sooner" || albums[1].rg.ID != "later" {
		t.Errorf("albums = %+v", albums)
	}
	if eps := sh.snapshot("eps"); len(eps) != 1 || eps[0].rg.ID != "ep" {
		t.Errorf("eps = %+v", eps)
	}
}

func TestPruneDropsReleasesThatHavePassed(t *testing.T) {
	sh := newTestShared()
	sh.releases["past"] = entry("past", "album", time.Now().AddDate(0, 0, -2).Format("2006-01-02"))
	sh.releases["future"] = entry("future", "album", time.Now().AddDate(0, 0, 2).Format("2006-01-02"))
	sh.prune()
	if _, ok := sh.releases["past"]; ok {
		t.Error("past release should have been pruned")
	}
	if _, ok := sh.releases["future"]; !ok {
		t.Error("future release should survive prune")
	}
}

// fixture fakes both Spotify's Web API and MusicBrainz for a full poll.
type fixture struct {
	mu            sync.Mutex
	spotifyHits   atomic.Int32
	spotifyStatus int // non-zero: every Spotify request fails with it
	retryAfter    string
	mbStatus      int // non-zero: every release-group query fails with it
	queries       []string
	nameSearches  []string
}

func (f *fixture) shared(t *testing.T) *shared {
	t.Helper()
	sp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.spotifyHits.Add(1)
		if f.spotifyStatus != 0 {
			if f.retryAfter != "" {
				w.Header().Set("Retry-After", f.retryAfter)
			}
			w.WriteHeader(f.spotifyStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"artists": map[string]any{
			"items": []map[string]any{
				{"id": "sp1", "name": "One"}, {"id": "sp2", "name": "Two"}, {"id": "sp3", "name": "Three"},
			},
			"cursors": map[string]any{"after": ""},
		}})
	}))
	t.Cleanup(sp.Close)

	future := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	past := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	nextMonth := time.Now().AddDate(0, 1, 0).Format("2006-01")
	mb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/url":
			_ = json.NewEncoder(w).Encode(map[string]any{"urls": []map[string]any{{
				"resource":  "https://open.spotify.com/artist/sp1",
				"relations": []map[string]any{{"target-type": "artist", "artist": map[string]any{"id": "mb1"}}},
			}}})
		case "/artist":
			q := r.URL.Query().Get("query")
			f.nameSearches = append(f.nameSearches, q)
			var artists []map[string]any
			if strings.Contains(q, "Two") {
				artists = append(artists, map[string]any{"id": "mb2", "name": "Two"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"artists": artists})
		case "/release-group":
			f.queries = append(f.queries, r.URL.Query().Get("query"))
			if f.mbStatus != 0 {
				w.WriteHeader(f.mbStatus)
				return
			}
			rg := func(id, typ, date string) map[string]any {
				return map[string]any{"id": id, "title": id, "primary-type": typ, "first-release-date": date,
					"artist-credit": []map[string]any{{"name": "One", "artist": map[string]any{"id": "mb1"}}}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 4, "release-groups": []map[string]any{
				rg("album", "Album", future), rg("ep", "EP", nextMonth),
				rg("single", "Single", future), rg("old", "Album", past),
			}})
		default:
			t.Errorf("unexpected MusicBrainz path %s", r.URL.Path)
		}
	}))
	t.Cleanup(mb.Close)

	client := spotifyapi.NewClient()
	client.APIBase = sp.URL
	mbc := musicbrainz.NewClient()
	mbc.Base = mb.URL
	mbc.Spacing = 0
	mbc.RetryWaits = nil
	sh := newShared(client, mbc, slog.Default())
	sh.tok, sh.tokExp = "at-1", wallNow().Add(time.Hour) // skip the token refresh
	return sh
}

func TestPollEndToEnd(t *testing.T) {
	f := &fixture{}
	sh := f.shared(t)

	if err := sh.maybePoll(context.Background()); err != nil {
		t.Fatalf("maybePoll: %v", err)
	}
	if got := sh.snapshot("album"); len(got) != 1 || got[0].rg.ID != "album" {
		t.Errorf("albums = %+v (singles and past records must be dropped)", got)
	}
	if got := sh.snapshot("eps"); len(got) != 1 || got[0].rg.ID != "ep" || got[0].precision != "month" {
		t.Errorf("eps = %+v", got)
	}
	if sh.mbids["sp1"] != "mb1" || sh.mbids["sp2"] != "mb2" || sh.mbids["sp3"] != "" {
		t.Errorf("mbids = %v, want sp1 by link, sp2 by name, sp3 unmatched", sh.mbids)
	}
	if len(f.queries) != 1 || !strings.Contains(f.queries[0], "arid:mb1") {
		t.Errorf("first sweep queries = %v", f.queries)
	}

	// Next poll: the name-matched artist makes a sweep due right away, and
	// the unmatched one isn't searched again so soon.
	sh.lastPollAt = wallNow().Add(-pollInterval - time.Second)
	if err := sh.maybePoll(context.Background()); err != nil {
		t.Fatalf("second maybePoll: %v", err)
	}
	if len(f.queries) != 2 || !strings.Contains(f.queries[1], "arid:mb2") {
		t.Errorf("second sweep should include the newly mapped artist: %v", f.queries)
	}
	if len(f.nameSearches) != 2 {
		t.Errorf("name searches = %v, want Two and Three once each", f.nameSearches)
	}
	if f.spotifyHits.Load() != 1 {
		t.Errorf("followed artists should be fetched once a day, got %d Spotify requests", f.spotifyHits.Load())
	}

	// Third poll: nothing due, so MusicBrainz isn't touched at all.
	sh.lastPollAt = wallNow().Add(-pollInterval - time.Second)
	_ = sh.maybePoll(context.Background())
	if len(f.queries) != 2 {
		t.Errorf("no sweep should run before sweepInterval, got %d queries", len(f.queries))
	}
}

func TestMaybePollSkipsWithinInterval(t *testing.T) {
	f := &fixture{}
	sh := f.shared(t)
	_ = sh.maybePoll(context.Background())
	_ = sh.maybePoll(context.Background()) // the other tab, or a double refresh click
	if f.spotifyHits.Load() != 1 || len(f.queries) != 1 {
		t.Errorf("second call within pollInterval must be a no-op: %d spotify, %d mb", f.spotifyHits.Load(), len(f.queries))
	}
}

func TestFailedSweepKeepsLastGoodReleases(t *testing.T) {
	f := &fixture{}
	sh := f.shared(t)
	if err := sh.maybePoll(context.Background()); err != nil {
		t.Fatal(err)
	}

	f.mbStatus = http.StatusInternalServerError
	sh.sweepDue = true
	sh.lastPollAt = wallNow().Add(-pollInterval - time.Second)
	if err := sh.maybePoll(context.Background()); err != nil {
		t.Errorf("a failed re-sweep shouldn't error while last sweep's data is still shown: %v", err)
	}
	if len(sh.snapshot("album")) != 1 {
		t.Error("the previous sweep's releases should be kept")
	}
}

func TestFirstSweepFailureIsReported(t *testing.T) {
	f := &fixture{mbStatus: http.StatusServiceUnavailable}
	sh := f.shared(t)
	if err := sh.maybePoll(context.Background()); err == nil {
		t.Error("with nothing ever fetched, a MusicBrainz failure must surface")
	}
}

func TestSpotify429BlocksSpotifyUntilRetryAfter(t *testing.T) {
	f := &fixture{spotifyStatus: http.StatusTooManyRequests, retryAfter: "74442"}
	sh := f.shared(t)

	if err := sh.maybePoll(context.Background()); err == nil {
		t.Fatal("no artist list at all should be an error")
	}
	if d := time.Until(sh.blockedUntil); d < 20*time.Hour {
		t.Errorf("blockedUntil should honor Retry-After, only %s away", d)
	}

	sh.lastPollAt = wallNow().Add(-pollInterval - time.Second)
	_ = sh.maybePoll(context.Background())
	if f.spotifyHits.Load() != 1 {
		t.Errorf("no Spotify request may be made while blocked, got %d", f.spotifyHits.Load())
	}
}

func TestSpotify401DropsCachedToken(t *testing.T) {
	f := &fixture{spotifyStatus: http.StatusUnauthorized}
	sh := f.shared(t)
	_ = sh.maybePoll(context.Background())
	if sh.tok != "" {
		t.Error("a rejected access token should be dropped so the next poll refreshes it")
	}
}

func TestStaleArtistListStillUsedWhenSpotifyFails(t *testing.T) {
	f := &fixture{}
	sh := f.shared(t)
	if err := sh.maybePoll(context.Background()); err != nil {
		t.Fatal(err)
	}

	f.spotifyStatus = http.StatusInternalServerError
	sh.artistsCheckedAt = wallNow().Add(-artistListTTL - time.Hour)
	sh.lastPollAt = wallNow().Add(-pollInterval - time.Second)
	if err := sh.maybePoll(context.Background()); err != nil {
		t.Errorf("yesterday's artist list is good enough, got %v", err)
	}
	if len(sh.artists) != 3 {
		t.Errorf("artists = %d, want the previous 3 kept", len(sh.artists))
	}
}
