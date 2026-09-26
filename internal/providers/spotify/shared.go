package spotify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maxbasque/quarks/internal/musicbrainz"
	"github.com/maxbasque/quarks/internal/spotifyapi"
)

// Spotify supplies only the followed-artist list (a handful of paginated
// calls a day). Release data comes from MusicBrainz, which — unlike Spotify's
// catalog — lists records that are announced but not out yet, and can answer
// "what's upcoming for these 50 artists" in one query. The previous design
// polled Spotify's per-artist albums endpoint and earned a ~20h rate-limit
// penalty (2026-09-24) while still rarely finding anything before release day.

// pollInterval is the minimum gap between two actual polls (see maybePoll) —
// not a ticker period. There is no background goroutine; a poll only happens
// synchronously inside Fetch, triggered by the widget's own TTL or a manual
// refresh click. This floor is what stops two widgets sharing one *shared (say,
// an albums-only and an EPs-only widget), or repeated refresh clicks, from polling
// back-to-back.
const pollInterval = 10 * time.Minute

// artistListTTL is how long the followed-artist list is trusted before a
// refetch from Spotify.
const artistListTTL = 24 * time.Hour

// sweepInterval is how often the upcoming releases of every mapped artist are
// re-queried from MusicBrainz. A full sweep is one request per 50 artists.
const sweepInterval = 6 * time.Hour

// nameRetryAfter is how long an artist that couldn't be matched by name stays
// skipped before it's searched again — someone may have added it to
// MusicBrainz, or its Spotify link, in the meantime.
const nameRetryAfter = 7 * 24 * time.Hour

// pollBudget caps one poll's wall time when the caller's context has no
// deadline; with one, the poll stops pollMargin short of it. MusicBrainz
// allows one request a second, so this bounds a poll to a couple dozen.
const (
	pollBudget = 25 * time.Second
	pollMargin = 2 * time.Second
)

// excludedSecondary are MusicBrainz secondary types that aren't new material
// from the artist, so they're never shown even when their primary type is
// Album or EP.
var excludedSecondary = map[string]bool{"Compilation": true, "Remix": true, "DJ-mix": true}

// releaseEntry is one cached upcoming release.
type releaseEntry struct {
	rg        musicbrainz.ReleaseGroup
	date      time.Time // start of the announced period, for sorting
	precision string    // "day" | "month" | "year"
	class     string    // "album" | "eps"
}

// shared is one Spotify account's release cache — one per distinct refresh
// token, not one per widget instance. Two widgets (say, albums-only and EPs-only
// widgets) reading the same account converge on the same *shared via acquire, so
// whichever one's Fetch runs first for a given poll window does the work and
// pollMu/lastPollAt make the other a no-op. The cache survives config reloads
// as long as the refresh token doesn't change.
type shared struct {
	client *spotifyapi.Client
	mb     *musicbrainz.Client
	creds  spotifyapi.Credentials
	log    *slog.Logger

	tokMu  sync.Mutex
	tok    string
	tokExp time.Time

	pollMu     sync.Mutex // serializes maybePoll across concurrent Fetch calls
	lastPollAt time.Time
	// blockedUntil is when Spotify's last 429 Retry-After expires. Until
	// then no Spotify request is made. Guarded by pollMu.
	blockedUntil time.Time
	// pollErr is the last poll's failure (nil if it got useful work done),
	// reported by every widget's Fetch until the next poll. Guarded by pollMu.
	pollErr error

	mu               sync.RWMutex
	artists          []spotifyapi.Artist
	artistsCheckedAt time.Time
	mbids            map[string]string    // spotify artist ID -> MusicBrainz artist ID
	urlTried         map[string]bool      // looked up by Spotify link since the last artist refresh
	nameTriedAt      map[string]time.Time // last failed name search
	releases         map[string]releaseEntry
	lastSweepAt      time.Time
	sweepDue         bool // new artists mapped since the last sweep
}

var (
	sharedMu sync.Mutex
	shareds  = map[string]*shared{}
)

// acquire returns the shared cache for creds, creating it on first use. The
// map key is a hash of the refresh token, never the token itself.
func acquire(creds spotifyapi.Credentials, log *slog.Logger) *shared {
	key := cacheKey(creds.RefreshToken)

	sharedMu.Lock()
	defer sharedMu.Unlock()
	sh, ok := shareds[key]
	if !ok {
		sh = newShared(spotifyapi.NewClient(), musicbrainz.NewClient(), log)
		sh.creds = creds
		shareds[key] = sh
	}
	return sh
}

func newShared(client *spotifyapi.Client, mb *musicbrainz.Client, log *slog.Logger) *shared {
	return &shared{
		client:      client,
		mb:          mb,
		log:         log,
		mbids:       map[string]string{},
		urlTried:    map[string]bool{},
		nameTriedAt: map[string]time.Time{},
		releases:    map[string]releaseEntry{},
	}
}

func cacheKey(refreshToken string) string {
	sum := sha256.Sum256([]byte(refreshToken))
	return hex.EncodeToString(sum[:])
}

// wallNow is time.Now without its monotonic reading, so comparisons against
// it use the wall clock. Go's monotonic clock stops while the machine is
// suspended; with it, an access token cached before a night's sleep still
// looked valid the next morning and every request 401'd.
func wallNow() time.Time { return time.Now().Round(0) }

// maybePoll runs one poll if at least pollInterval has passed since the last
// one, otherwise it's a no-op. Called synchronously from Fetch, so it shares
// Fetch's context deadline. Returns the error Fetch should report, which
// persists across skipped calls so both widgets show the same state.
func (s *shared) maybePoll(ctx context.Context) error {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()

	now := wallNow()
	if !s.lastPollAt.IsZero() && now.Sub(s.lastPollAt) < pollInterval {
		return s.pollErr
	}
	s.lastPollAt = now

	deadline := now.Add(pollBudget)
	if dl, ok := ctx.Deadline(); ok && dl.Add(-pollMargin).Before(deadline) {
		deadline = dl.Add(-pollMargin)
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	s.pollErr = s.poll(ctx)
	return s.pollErr
}

// poll does whatever's due, cheapest and most useful first:
//
//  1. refresh the followed-artist list from Spotify (daily);
//  2. map new artists to MusicBrainz by their Spotify link (100 per request);
//  3. sweep upcoming releases for every mapped artist (every sweepInterval,
//     or sooner once new artists are mapped);
//  4. spend what's left of the budget matching unlinked artists by name,
//     one request each, so a big first-time backlog trickles in over polls.
//
// Returns an error only when the poll leaves the widget with nothing
// trustworthy to show. Caller holds pollMu.
func (s *shared) poll(ctx context.Context) error {
	if err := s.ensureArtists(ctx); err != nil {
		s.log.Warn("spotify: refresh followed artists failed", "err", err)
		s.mu.RLock()
		have := len(s.artists) > 0
		s.mu.RUnlock()
		if !have {
			return fmt.Errorf("spotify: refresh followed artists: %w", err)
		}
		// Carry on with yesterday's list; releases come from MusicBrainz anyway.
	}

	if err := s.resolveByURL(ctx); err != nil {
		s.log.Warn("musicbrainz: artist lookup failed", "err", err)
		if errors.Is(err, musicbrainz.ErrRateLimited) {
			return err
		}
	}

	s.mu.RLock()
	due := s.sweepDue || wallNow().Sub(s.lastSweepAt) >= sweepInterval
	s.mu.RUnlock()
	if due {
		if err := s.sweep(ctx); err != nil {
			s.log.Warn("musicbrainz: release sweep failed", "err", err)
			s.mu.RLock()
			swept := !s.lastSweepAt.IsZero()
			s.mu.RUnlock()
			if !swept || errors.Is(err, musicbrainz.ErrRateLimited) {
				return fmt.Errorf("musicbrainz: upcoming releases: %w", err)
			}
			return nil // keep showing the last complete sweep
		}
	}

	if err := s.resolveByName(ctx); err != nil && ctx.Err() == nil {
		s.log.Warn("musicbrainz: artist name search failed", "err", err)
	}
	s.prune()
	return nil
}

// ensureArtists refetches the full followed-artist list if it hasn't been
// checked in artistListTTL (or ever). Honors Spotify's Retry-After.
func (s *shared) ensureArtists(ctx context.Context) error {
	s.mu.RLock()
	stale := wallNow().Sub(s.artistsCheckedAt) > artistListTTL
	s.mu.RUnlock()
	if !stale {
		return nil
	}
	if now := wallNow(); now.Before(s.blockedUntil) {
		return fmt.Errorf("rate-limited by Spotify until %s", s.blockedUntil.Local().Format("Mon Jan 2 15:04"))
	}

	tok, err := s.accessToken(ctx)
	if err != nil {
		return err
	}

	var all []spotifyapi.Artist
	after := ""
	for {
		page, next, err := s.client.FollowedArtists(ctx, tok, after)
		if err != nil {
			s.noteErr(err)
			return err
		}
		all = append(all, page...)
		if next == "" {
			break
		}
		after = next
	}

	s.mu.Lock()
	s.artists = all
	s.artistsCheckedAt = wallNow()
	s.urlTried = map[string]bool{} // links get added over time: retry the unmapped
	s.mu.Unlock()
	return nil
}

// noteErr reacts to a Spotify error that affects every later request: a 429
// blocks Spotify calls for its Retry-After, a 401 drops the cached access
// token so the next attempt mints a fresh one. Caller holds pollMu.
func (s *shared) noteErr(err error) {
	var rl *spotifyapi.RateLimitedError
	switch {
	case errors.As(err, &rl):
		s.blockedUntil = wallNow().Add(rl.RetryAfter)
		s.log.Warn("spotify: rate limited, pausing requests", "retry_after", rl.RetryAfter, "until", s.blockedUntil.Local())
	case errors.Is(err, spotifyapi.ErrUnauthorized):
		s.tokMu.Lock()
		s.tok = ""
		s.tokMu.Unlock()
	}
}

// accessToken returns a cached access token, refreshing it a minute before it
// actually expires.
func (s *shared) accessToken(ctx context.Context) (string, error) {
	s.tokMu.Lock()
	defer s.tokMu.Unlock()

	if s.tok != "" && wallNow().Before(s.tokExp) {
		return s.tok, nil
	}
	tok, expiresIn, err := s.client.RefreshAccessToken(ctx, s.creds)
	if err != nil {
		return "", err
	}
	s.tok = tok
	s.tokExp = wallNow().Add(time.Duration(expiresIn)*time.Second - time.Minute)
	return s.tok, nil
}

// resolveByURL maps every not-yet-mapped artist whose Spotify link hasn't
// been looked up since the last artist refresh.
func (s *shared) resolveByURL(ctx context.Context) error {
	s.mu.RLock()
	var todo []string
	for _, a := range s.artists {
		if s.mbids[a.ID] == "" && !s.urlTried[a.ID] {
			todo = append(todo, a.ID)
		}
	}
	s.mu.RUnlock()

	for len(todo) > 0 {
		n := min(len(todo), musicbrainz.MaxURLLookup)
		batch := todo[:n]
		todo = todo[n:]

		found, err := s.mb.ArtistsBySpotify(ctx, batch)
		if err != nil {
			return err
		}
		s.mu.Lock()
		for _, id := range batch {
			s.urlTried[id] = true
			if mbid := found[id]; mbid != "" {
				s.mbids[id] = mbid
				s.sweepDue = true
			}
		}
		s.mu.Unlock()
	}
	return nil
}

// resolveByName tries an exact-name search for artists the link lookup
// couldn't map, one request each, until the poll's deadline is near.
func (s *shared) resolveByName(ctx context.Context) error {
	now := wallNow()
	s.mu.RLock()
	var todo []spotifyapi.Artist
	for _, a := range s.artists {
		if s.mbids[a.ID] == "" && s.urlTried[a.ID] && now.Sub(s.nameTriedAt[a.ID]) >= nameRetryAfter {
			todo = append(todo, a)
		}
	}
	s.mu.RUnlock()

	for _, a := range todo {
		if dl, ok := ctx.Deadline(); ok && time.Until(dl) < 2*s.mb.Spacing {
			return nil // out of budget; the rest wait for the next poll
		}
		mbid, ok, err := s.mb.ArtistByName(ctx, a.Name)
		if err != nil {
			return err
		}
		s.mu.Lock()
		if ok {
			s.mbids[a.ID] = mbid
			s.sweepDue = true
		} else {
			s.nameTriedAt[a.ID] = wallNow()
		}
		s.mu.Unlock()
	}
	return nil
}

// sweep re-queries upcoming releases for every mapped artist. A complete
// sweep replaces the cache (so cancelled or re-dated records disappear); a
// partial one only adds to it.
func (s *shared) sweep(ctx context.Context) error {
	s.mu.RLock()
	seen := map[string]bool{}
	var ids []string
	for _, a := range s.artists {
		if mbid := s.mbids[a.ID]; mbid != "" && !seen[mbid] {
			seen[mbid] = true
			ids = append(ids, mbid)
		}
	}
	s.mu.RUnlock()

	now := time.Now()
	// Start the range a month back: a month-precision record due this month
	// is still upcoming, and upcoming() does the exact filtering.
	from := now.AddDate(0, -1, 0)
	fresh := map[string]releaseEntry{}
	var err error
	for len(ids) > 0 {
		n := min(len(ids), musicbrainz.MaxArtistsPerQuery)
		var rgs []musicbrainz.ReleaseGroup
		rgs, err = s.mb.UpcomingReleaseGroups(ctx, ids[:n], from)
		if err != nil {
			break
		}
		ids = ids[n:]
		for _, rg := range rgs {
			if e, ok := toEntry(rg, now); ok {
				fresh[rg.ID] = e
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		for id, e := range fresh {
			s.releases[id] = e
		}
		return err
	}
	s.releases = fresh
	s.lastSweepAt = wallNow()
	s.sweepDue = false
	return nil
}

// toEntry classifies rg and reports whether it belongs on the dashboard: an
// Album or EP, not a compilation/remix, still upcoming as of now.
func toEntry(rg musicbrainz.ReleaseGroup, now time.Time) (releaseEntry, bool) {
	class := classify(rg)
	if class == "" {
		return releaseEntry{}, false
	}
	date, precision, ok := parseReleaseDate(rg.FirstReleaseDate)
	if !ok || !upcoming(date, precision, now) {
		return releaseEntry{}, false
	}
	return releaseEntry{rg: rg, date: date, precision: precision, class: class}, true
}

// classify buckets a release group as "album" or "eps" from MusicBrainz's own
// primary type, or "" for anything this widget doesn't show (singles,
// broadcasts, compilations, remix records).
func classify(rg musicbrainz.ReleaseGroup) string {
	for _, t := range rg.SecondaryTypes {
		if excludedSecondary[t] {
			return ""
		}
	}
	switch rg.PrimaryType {
	case "Album":
		return "album"
	case "EP":
		return "eps"
	}
	return ""
}

// parseReleaseDate parses a MusicBrainz date, whose precision is implied by
// its length: "2026-10-13", "2026-10" or "2026".
func parseReleaseDate(s string) (time.Time, string, bool) {
	for _, f := range []struct{ layout, precision string }{
		{"2006-01-02", "day"}, {"2006-01", "month"}, {"2006", "year"},
	} {
		if t, err := time.ParseInLocation(f.layout, s, time.Local); err == nil {
			return t, f.precision, true
		}
	}
	return time.Time{}, "", false
}

// upcoming reports whether a release dated (start, precision) is still ahead:
// a day or month counts until it's over. A bare year only counts if it's a
// future year — a current-year "2026" is far more often an undated catalog
// entry than an announcement.
func upcoming(start time.Time, precision string, now time.Time) bool {
	switch precision {
	case "day":
		return start.AddDate(0, 0, 1).After(now)
	case "month":
		return start.AddDate(0, 1, 0).After(now)
	case "year":
		return start.Year() > now.Year()
	}
	return false
}

// prune drops cached releases whose date has passed since the last sweep.
func (s *shared) prune() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range s.releases {
		if !upcoming(r.date, r.precision, now) {
			delete(s.releases, id)
		}
	}
}

// snapshot returns every cached upcoming release of class include ("all" for
// every class), sorted
// soonest-first. No network call itself.
func (s *shared) snapshot(include string) []releaseEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]releaseEntry, 0, len(s.releases))
	for _, r := range s.releases {
		if include == "all" || r.class == include {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].date.Equal(out[j].date) {
			return out[i].date.Before(out[j].date)
		}
		return strings.ToLower(out[i].rg.ArtistCredit) < strings.ToLower(out[j].rg.ArtistCredit)
	})
	return out
}
