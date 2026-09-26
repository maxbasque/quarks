// Package musicbrainz is a thin, dependency-free client for the pieces of the
// MusicBrainz web service this app needs: mapping Spotify artist IDs to
// MusicBrainz artist IDs, and finding release groups with a future first
// release date for many artists at once. Unlike Spotify's catalog API,
// MusicBrainz lists announced-but-unreleased records, and it needs no auth.
//
// MusicBrainz allows one request per second per client and requires a
// descriptive User-Agent; Client enforces both itself so callers can't burst.
package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxURLLookup is how many resources one ArtistsBySpotify request can carry.
// Verified live (2026-09-26): at 101 the service silently returns no matches
// at all rather than an error.
const MaxURLLookup = 100

// MaxArtistsPerQuery bounds how many artist IDs UpcomingReleaseGroups ORs
// into one search query, keeping the request URL a comfortable size.
// 60 was verified live; 50 leaves headroom.
const MaxArtistsPerQuery = 50

const userAgent = "quarks/0.1 ( https://github.com/maxbasque/quarks )"

// ReleaseGroup is one upcoming record. FirstReleaseDate's precision varies:
// "2026-10-13", "2026-10" or just "2026".
type ReleaseGroup struct {
	ID               string
	Title            string
	PrimaryType      string   // "Album" | "EP" | "Single" | "Broadcast" | "Other" | ""
	SecondaryTypes   []string // "Live", "Compilation", "Remix", "Soundtrack", …
	FirstReleaseDate string
	ArtistCredit     string   // display credit, e.g. "Artist A & Artist B"
	ArtistIDs        []string // MusicBrainz IDs of every credited artist
}

// URL is the release group's MusicBrainz page.
func (rg ReleaseGroup) URL() string { return "https://musicbrainz.org/release-group/" + rg.ID }

// ErrRateLimited is a 503 from MusicBrainz that persisted through get's
// retries — its signal for "slow down".
var ErrRateLimited = errors.New("musicbrainz: rate limited")

// Client talks to the MusicBrainz web service. Base is overridable so tests
// can point it at a local fixture server; Spacing is the minimum gap between
// two requests (the service's documented limit is 1/s; a little slower
// draws noticeably fewer 503s in practice). RetryWaits are the pauses before
// retrying a 503: MusicBrainz 503s aren't only about this client's pace — its
// anonymous pool is shared and it sheds load under pressure (seen live
// 2026-09-26 at a steady 1 req/1.1s), so a short back-off usually gets through.
type Client struct {
	HTTP       *http.Client
	Base       string
	Spacing    time.Duration
	RetryWaits []time.Duration

	mu   sync.Mutex
	next time.Time // earliest time the next request may start
}

func NewClient() *Client {
	return &Client{
		HTTP:       &http.Client{Timeout: 15 * time.Second},
		Base:       "https://musicbrainz.org/ws/2",
		Spacing:    1500 * time.Millisecond,
		RetryWaits: []time.Duration{2 * time.Second, 5 * time.Second},
	}
}

// ArtistsBySpotify maps Spotify artist IDs to MusicBrainz artist IDs via the
// Spotify links MusicBrainz stores on artist pages. IDs with no link are
// simply absent from the result. At most MaxURLLookup IDs per call.
func (c *Client) ArtistsBySpotify(ctx context.Context, spotifyIDs []string) (map[string]string, error) {
	if len(spotifyIDs) == 0 {
		return map[string]string{}, nil
	}
	if len(spotifyIDs) > MaxURLLookup {
		return nil, fmt.Errorf("musicbrainz: %d ids exceeds the %d-resource lookup limit", len(spotifyIDs), MaxURLLookup)
	}
	q := url.Values{"inc": {"artist-rels"}, "fmt": {"json"}}
	byURL := make(map[string]string, len(spotifyIDs))
	for _, id := range spotifyIDs {
		u := "https://open.spotify.com/artist/" + id
		q.Add("resource", u)
		byURL[u] = id
	}

	type urlEntity struct {
		Resource  string `json:"resource"`
		Relations []struct {
			TargetType string `json:"target-type"`
			Artist     struct {
				ID string `json:"id"`
			} `json:"artist"`
		} `json:"relations"`
	}
	var entities []urlEntity
	if len(spotifyIDs) == 1 {
		// A single resource comes back as the bare entity, not a list — and
		// as a 404 when MusicBrainz has no such link.
		var one urlEntity
		err := c.get(ctx, "/url?"+q.Encode(), &one)
		if errors.Is(err, errNotFound) {
			return map[string]string{}, nil
		}
		if err != nil {
			return nil, err
		}
		entities = append(entities, one)
	} else {
		var many struct {
			URLs []urlEntity `json:"urls"`
		}
		if err := c.get(ctx, "/url?"+q.Encode(), &many); err != nil && !errors.Is(err, errNotFound) {
			return nil, err
		}
		entities = many.URLs
	}

	out := make(map[string]string, len(entities))
	for _, e := range entities {
		sid, ok := byURL[e.Resource]
		if !ok {
			continue
		}
		for _, rel := range e.Relations {
			if rel.TargetType == "artist" && rel.Artist.ID != "" {
				out[sid] = rel.Artist.ID
				break
			}
		}
	}
	return out, nil
}

// ArtistByName looks an artist up by exact name, for followed artists whose
// MusicBrainz page carries no Spotify link. It only returns a match when it's
// unambiguous — exactly one artist whose name equals name (case-insensitively)
// — because a wrong guess would put a stranger's releases on the dashboard.
func (c *Client) ArtistByName(ctx context.Context, name string) (string, bool, error) {
	q := url.Values{
		"query": {`artist:"` + escapeQuery(name) + `"`},
		"limit": {"10"},
		"fmt":   {"json"},
	}
	var res struct {
		Artists []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"artists"`
	}
	if err := c.get(ctx, "/artist?"+q.Encode(), &res); err != nil {
		return "", false, err
	}
	match := ""
	for _, a := range res.Artists {
		if !strings.EqualFold(strings.TrimSpace(a.Name), strings.TrimSpace(name)) {
			continue
		}
		if match != "" {
			return "", false, nil // two artists share the name: don't guess
		}
		match = a.ID
	}
	return match, match != "", nil
}

// UpcomingReleaseGroups returns release groups credited to any of artistIDs
// whose first release date is on or after from. At most MaxArtistsPerQuery
// artists per call; pages through results itself.
//
// Year- or month-only dates match the range generously (a "2026" record
// matches any 2026 from), so callers should re-check dates themselves.
func (c *Client) UpcomingReleaseGroups(ctx context.Context, artistIDs []string, from time.Time) ([]ReleaseGroup, error) {
	if len(artistIDs) == 0 {
		return nil, nil
	}
	if len(artistIDs) > MaxArtistsPerQuery {
		return nil, fmt.Errorf("musicbrainz: %d artists exceeds the %d-per-query limit", len(artistIDs), MaxArtistsPerQuery)
	}
	terms := make([]string, len(artistIDs))
	for i, id := range artistIDs {
		terms[i] = "arid:" + id
	}
	query := "(" + strings.Join(terms, " OR ") + ") AND firstreleasedate:[" + from.Format("2006-01-02") + " TO *]"

	const pageSize = 100
	const maxPages = 5 // a few hundred upcoming records for 50 artists is already implausible
	var out []ReleaseGroup
	for page := 0; page < maxPages; page++ {
		q := url.Values{
			"query":  {query},
			"limit":  {strconv.Itoa(pageSize)},
			"offset": {strconv.Itoa(page * pageSize)},
			"fmt":    {"json"},
		}
		var res struct {
			Count         int `json:"count"`
			ReleaseGroups []struct {
				ID               string   `json:"id"`
				Title            string   `json:"title"`
				PrimaryType      string   `json:"primary-type"`
				SecondaryTypes   []string `json:"secondary-types"`
				FirstReleaseDate string   `json:"first-release-date"`
				ArtistCredit     []struct {
					Name       string `json:"name"`
					JoinPhrase string `json:"joinphrase"`
					Artist     struct {
						ID string `json:"id"`
					} `json:"artist"`
				} `json:"artist-credit"`
			} `json:"release-groups"`
		}
		if err := c.get(ctx, "/release-group?"+q.Encode(), &res); err != nil {
			return nil, err
		}
		for _, r := range res.ReleaseGroups {
			rg := ReleaseGroup{
				ID:               r.ID,
				Title:            r.Title,
				PrimaryType:      r.PrimaryType,
				SecondaryTypes:   r.SecondaryTypes,
				FirstReleaseDate: r.FirstReleaseDate,
			}
			var credit strings.Builder
			for _, ac := range r.ArtistCredit {
				credit.WriteString(ac.Name + ac.JoinPhrase)
				rg.ArtistIDs = append(rg.ArtistIDs, ac.Artist.ID)
			}
			rg.ArtistCredit = credit.String()
			out = append(out, rg)
		}
		if len(res.ReleaseGroups) < pageSize || (page+1)*pageSize >= res.Count {
			break
		}
	}
	return out, nil
}

var errNotFound = errors.New("musicbrainz: not found")

// get does one GET against Base+path, retrying 503s after RetryWaits while
// the context's deadline allows.
func (c *Client) get(ctx context.Context, path string, v any) error {
	err := c.getOnce(ctx, path, v)
	for _, d := range c.RetryWaits {
		if !errors.Is(err, ErrRateLimited) {
			return err
		}
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) < d+c.Spacing {
			return err
		}
		t := time.NewTimer(d)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return err
		}
		err = c.getOnce(ctx, path, v)
	}
	return err
}

// getOnce waits for its rate-limit slot, then does one GET.
func (c *Client) getOnce(ctx context.Context, path string, v any) error {
	if err := c.wait(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return errNotFound
	case http.StatusServiceUnavailable, http.StatusTooManyRequests:
		return ErrRateLimited
	default:
		return fmt.Errorf("musicbrainz: http %d on %s: %s", resp.StatusCode, path, trim(body))
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("musicbrainz: parse response from %s: %w", path, err)
	}
	return nil
}

// wait blocks until this client may send its next request, reserving the
// slot after it so concurrent callers queue up Spacing apart.
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	now := time.Now()
	start := c.next
	if start.Before(now) {
		start = now
	}
	c.next = start.Add(c.Spacing)
	c.mu.Unlock()

	d := time.Until(start)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// escapeQuery backslash-escapes Lucene's special characters so an artist name
// like "AC/DC" or "Sunn O)))" is searched literally.
func escapeQuery(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`+-&|!(){}[]^"~*?:\/`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func trim(b []byte) string {
	const max = 300
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
