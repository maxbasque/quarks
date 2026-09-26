package musicbrainz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewClient()
	c.Base = srv.URL
	c.Spacing = 0
	c.RetryWaits = []time.Duration{0, 0}
	return c
}

func TestArtistsBySpotifyBatch(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); !strings.Contains(ua, "quarks") {
			t.Errorf("User-Agent = %q, MusicBrainz requires a descriptive one", ua)
		}
		if got := r.URL.Query()["resource"]; len(got) != 2 {
			t.Errorf("resources = %v", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"urls": []map[string]any{{
			"resource": "https://open.spotify.com/artist/sp1",
			"relations": []map[string]any{
				{"target-type": "artist", "artist": map[string]any{"id": "mb1"}},
			},
		}}})
	})

	got, err := c.ArtistsBySpotify(context.Background(), []string{"sp1", "sp2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["sp1"] != "mb1" {
		t.Errorf("got %v, want only sp1 -> mb1", got)
	}
}

func TestArtistsBySpotifySingleNotFound(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Not Found"}`))
	})
	got, err := c.ArtistsBySpotify(context.Background(), []string{"nope"})
	if err != nil || len(got) != 0 {
		t.Errorf("a missing link is not an error: got %v, %v", got, err)
	}
}

func TestArtistsBySpotifySingleIsBareEntity(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":  "https://open.spotify.com/artist/sp1",
			"relations": []map[string]any{{"target-type": "artist", "artist": map[string]any{"id": "mb1"}}},
		})
	})
	got, err := c.ArtistsBySpotify(context.Background(), []string{"sp1"})
	if err != nil || got["sp1"] != "mb1" {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestArtistsBySpotifyRejectsOversizedBatch(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("must not send") })
	if _, err := c.ArtistsBySpotify(context.Background(), make([]string, MaxURLLookup+1)); err == nil {
		t.Error("expected an error: MusicBrainz silently returns nothing past the limit")
	}
}

func TestArtistByNameRequiresUnambiguousExactMatch(t *testing.T) {
	resp := map[string][]map[string]any{
		"Radiohead": {{"id": "rh", "name": "Radiohead"}, {"id": "x", "name": "DJ Radiohead"}},
		"Ghost":     {{"id": "g1", "name": "Ghost"}, {"id": "g2", "name": "ghost"}},
		"Nobody":    {{"id": "n", "name": "Nobody Special"}},
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		name := strings.TrimSuffix(strings.TrimPrefix(q, `artist:"`), `"`)
		_ = json.NewEncoder(w).Encode(map[string]any{"artists": resp[name]})
	})

	if id, ok, _ := c.ArtistByName(context.Background(), "Radiohead"); !ok || id != "rh" {
		t.Errorf("Radiohead: %q %v", id, ok)
	}
	if _, ok, _ := c.ArtistByName(context.Background(), "Ghost"); ok {
		t.Error("two exact matches must not be guessed between")
	}
	if _, ok, _ := c.ArtistByName(context.Background(), "Nobody"); ok {
		t.Error("a partial match must not count")
	}
}

func TestEscapeQuery(t *testing.T) {
	if got := escapeQuery(`AC/DC "live"`); got != `AC\/DC \"live\"` {
		t.Errorf("escapeQuery = %q", got)
	}
}

func TestUpcomingReleaseGroupsQueryAndParse(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		if q != "(arid:a1 OR arid:a2) AND firstreleasedate:[2026-09-01 TO *]" {
			t.Errorf("query = %q", q)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": 1,
			"release-groups": []map[string]any{{
				"id": "rg1", "title": "New One", "primary-type": "Album",
				"secondary-types": []string{"Live"}, "first-release-date": "2026-10-13",
				"artist-credit": []map[string]any{
					{"name": "A", "joinphrase": " & ", "artist": map[string]any{"id": "a1"}},
					{"name": "B", "joinphrase": "", "artist": map[string]any{"id": "b"}},
				},
			}},
		})
	})

	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	got, err := c.UpcomingReleaseGroups(context.Background(), []string{"a1", "a2"}, from)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d", len(got))
	}
	rg := got[0]
	if rg.Title != "New One" || rg.PrimaryType != "Album" || rg.FirstReleaseDate != "2026-10-13" ||
		rg.ArtistCredit != "A & B" || len(rg.SecondaryTypes) != 1 || len(rg.ArtistIDs) != 2 {
		t.Errorf("unexpected %+v", rg)
	}
	if rg.URL() != "https://musicbrainz.org/release-group/rg1" {
		t.Errorf("URL = %q", rg.URL())
	}
}

func TestUpcomingReleaseGroupsPaginates(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		n := 100
		if r.URL.Query().Get("offset") == "100" {
			n = 30
		}
		rgs := make([]map[string]any, n)
		for i := range rgs {
			rgs[i] = map[string]any{"id": "x"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 130, "release-groups": rgs})
	})
	got, err := c.UpcomingReleaseGroups(context.Background(), []string{"a1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 130 || calls.Load() != 2 {
		t.Errorf("got %d results in %d calls, want 130 in 2", len(got), calls.Load())
	}
}

func TestServiceUnavailableIsRetriedThenRateLimited(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := c.UpcomingReleaseGroups(context.Background(), []string{"a1"}, time.Now())
	if err != ErrRateLimited {
		t.Errorf("want ErrRateLimited, got %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 1 + 2 retries", calls.Load())
	}
}

func TestTransient503Recovers(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"artists": []map[string]any{{"id": "a", "name": "A"}}})
	})
	if id, ok, err := c.ArtistByName(context.Background(), "A"); err != nil || !ok || id != "a" {
		t.Errorf("got %q %v %v", id, ok, err)
	}
}

func TestNoRetryPastDeadline(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	c.RetryWaits = []time.Duration{2 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second) // shorter than the first real retry wait
	defer cancel()
	if _, _, err := c.ArtistByName(ctx, "A"); err != ErrRateLimited || calls.Load() != 1 {
		t.Errorf("err=%v calls=%d, want an immediate ErrRateLimited", err, calls.Load())
	}
}

func TestRequestsAreSpaced(t *testing.T) {
	var last, minGap atomic.Int64
	minGap.Store(int64(time.Hour))
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UnixNano()
		if prev := last.Swap(now); prev != 0 && now-prev < minGap.Load() {
			minGap.Store(now - prev)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"artists": []any{}})
	})
	c.Spacing = 50 * time.Millisecond
	for i := 0; i < 3; i++ {
		if _, _, err := c.ArtistByName(context.Background(), "x"); err != nil {
			t.Fatal(err)
		}
	}
	if gap := time.Duration(minGap.Load()); gap < 45*time.Millisecond {
		t.Errorf("requests only %s apart, want >= Spacing", gap)
	}
}
