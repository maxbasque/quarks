package spotify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/maxbasque/quarks/internal/spotifyapi"
)

// pollInterval is the minimum gap enforced between two actual polls of a
// batch of followed artists (see maybePoll) — not a ticker period. There is
// no background goroutine; a poll only happens synchronously inside Fetch,
// triggered by the widget's own TTL or a manual refresh click, so nothing
// runs against Spotify's API while nobody's looking at the dashboard. This
// floor is what stops two widgets sharing one *shared (an Albums tab and an
// EPs tab), or repeated manual refresh clicks, from polling back-to-back.
const pollInterval = 5 * time.Minute

// maxArtistsPerPoll bounds how many followed artists get checked in a single
// poll. Empirically confirmed (2026-09-20, against a real account with 226
// followed artists): ~50-75 sequential ArtistAlbums calls with zero spacing
// is enough to trigger sustained 429s from Spotify. A batch well under that,
// gated by pollInterval between polls, stays comfortably safe while covering
// even a large follow list in roughly an hour or two instead of the better
// part of a day.
const maxArtistsPerPoll = 15

// artistListTTL is how long the followed-artist list itself is trusted before
// a refetch — cheap (a handful of paginated calls) but no reason to redo it
// every poll.
const artistListTTL = 24 * time.Hour

// rotationSize returns how many artists to check in one poll: the whole list
// if it's smaller than the per-poll cap, otherwise the cap.
func rotationSize(total int) int {
	if total <= 0 {
		return 0
	}
	if total < maxArtistsPerPoll {
		return total
	}
	return maxArtistsPerPoll
}

// releaseEntry is one cached upcoming release: the raw Spotify facts plus its
// parsed release date (classification happens later, at read time, in
// snapshot — see classify).
type releaseEntry struct {
	album spotifyapi.Album
	date  time.Time
}

// snapshotItem is one classified release, as returned by snapshot.
type snapshotItem struct {
	album spotifyapi.Album
	date  time.Time
	class string // "album" | "eps" | "single"
}

// shared is one Spotify account's release cache — one per distinct refresh
// token, not one per widget instance. Two widgets (an Albums tab and an EPs
// tab) reading the same account converge on the same *shared via acquire, so
// they never double the API load between them: whichever one's Fetch runs
// first for a given poll window does the work, and pollMu/lastPollAt make the
// other one's concurrent or follow-up call a no-op. The cache survives config
// reloads as long as the refresh token doesn't change (acquire re-finds the
// same instance by credential fingerprint); only a genuine reconnect (a new
// refresh token) starts a fresh one.
type shared struct {
	client *spotifyapi.Client
	creds  spotifyapi.Credentials
	log    *slog.Logger

	tokMu  sync.Mutex
	tok    string
	tokExp time.Time

	pollMu     sync.Mutex // serializes maybePoll across concurrent Fetch calls
	lastPollAt time.Time

	mu               sync.RWMutex
	releases         map[string]releaseEntry // spotify album ID -> raw facts
	artists          []spotifyapi.Artist
	artistsCheckedAt time.Time
	cursor           int
}

var (
	sharedMu sync.Mutex
	shareds  = map[string]*shared{}
)

// acquire returns the shared cache for creds, creating it on first use. The
// map key is a hash of the refresh token, never the token itself, so it's
// safe to keep in memory/logs.
func acquire(creds spotifyapi.Credentials, log *slog.Logger) *shared {
	key := cacheKey(creds.RefreshToken)

	sharedMu.Lock()
	defer sharedMu.Unlock()
	sh, ok := shareds[key]
	if !ok {
		sh = &shared{
			client:   spotifyapi.NewClient(),
			creds:    creds,
			log:      log,
			releases: map[string]releaseEntry{},
		}
		shareds[key] = sh
	}
	return sh
}

func cacheKey(refreshToken string) string {
	sum := sha256.Sum256([]byte(refreshToken))
	return hex.EncodeToString(sum[:])
}

// maybePoll runs one poll (see pollSlice) if at least pollInterval has passed
// since the last one, otherwise it's a no-op — the actual rate-limit floor.
// Called synchronously from Fetch, so it shares Fetch's context deadline (the
// scheduler wraps every widget Fetch in a 30s timeout); a rotation-sized
// batch comfortably fits inside that.
func (s *shared) maybePoll(ctx context.Context) {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()

	if !s.lastPollAt.IsZero() && time.Since(s.lastPollAt) < pollInterval {
		return
	}
	s.lastPollAt = time.Now()
	s.pollSlice(ctx)
}

// pollSlice refreshes the followed-artist list if it's stale, checks one
// rotation-sized slice of the least-recently-checked artists, merges results
// into the cache, and prunes anything that's no longer upcoming.
func (s *shared) pollSlice(ctx context.Context) {
	if err := s.ensureArtists(ctx); err != nil {
		s.log.Warn("spotify: refresh followed artists failed", "err", err)
		return
	}

	s.mu.RLock()
	total := len(s.artists)
	s.mu.RUnlock()
	if total == 0 {
		return
	}

	tok, err := s.accessToken(ctx)
	if err != nil {
		s.log.Warn("spotify: refresh access token failed", "err", err)
		return
	}

	for i, n := 0, rotationSize(total); i < n; i++ {
		s.mu.Lock()
		if len(s.artists) == 0 {
			s.mu.Unlock()
			break
		}
		artist := s.artists[s.cursor%len(s.artists)]
		s.cursor++
		s.mu.Unlock()

		albums, err := s.client.ArtistAlbums(ctx, tok, artist.ID)
		if err != nil {
			s.log.Warn("spotify: fetch artist albums failed", "artist", artist.Name, "err", err)
			continue
		}
		s.ingest(artist.Name, albums)
	}
	s.prune()
}

// ensureArtists refetches the full followed-artist list if it hasn't been
// checked in artistListTTL (or ever).
func (s *shared) ensureArtists(ctx context.Context) error {
	s.mu.RLock()
	stale := time.Since(s.artistsCheckedAt) > artistListTTL
	s.mu.RUnlock()
	if !stale {
		return nil
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
	s.artistsCheckedAt = time.Now()
	if len(all) == 0 {
		s.cursor = 0
	} else {
		s.cursor %= len(all)
	}
	s.mu.Unlock()
	return nil
}

// accessToken returns a cached access token, refreshing it a minute before it
// actually expires.
func (s *shared) accessToken(ctx context.Context) (string, error) {
	s.tokMu.Lock()
	defer s.tokMu.Unlock()

	if s.tok != "" && time.Now().Before(s.tokExp) {
		return s.tok, nil
	}
	tok, expiresIn, err := s.client.RefreshAccessToken(ctx, s.creds)
	if err != nil {
		return "", err
	}
	s.tok = tok
	s.tokExp = time.Now().Add(time.Duration(expiresIn)*time.Second - time.Minute)
	return s.tok, nil
}

// ingest merges one artist's albums into the cache, keeping only releases
// that are actually upcoming (a future, parseable release date).
func (s *shared) ingest(artistName string, albums []spotifyapi.Album) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, al := range albums {
		date, ok := parseReleaseDate(al.ReleaseDate, al.ReleaseDatePrecision)
		if !ok || !date.After(now) {
			delete(s.releases, al.ID) // no longer upcoming (or never was) — drop if present
			continue
		}
		if al.ArtistName == "" {
			al.ArtistName = artistName
		}
		s.releases[al.ID] = releaseEntry{album: al, date: date}
	}
}

// prune drops any cached release whose date has passed since it was last
// checked — needed because most releases in the cache weren't touched this
// tick (only a rotation-sized slice of artists was).
func (s *shared) prune() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range s.releases {
		if !r.date.After(now) {
			delete(s.releases, id)
		}
	}
}

// snapshot returns every cached upcoming release classified as include
// ("album" | "eps" | "single"), sorted soonest-first. No network call itself
// — call maybePoll first if the cache should be refreshed.
func (s *shared) snapshot(include string, minEP, maxEP int) []snapshotItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]snapshotItem, 0, len(s.releases))
	for _, r := range s.releases {
		class := classify(r.album, minEP, maxEP)
		if class != include {
			continue
		}
		out = append(out, snapshotItem{album: r.album, date: r.date, class: class})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].date.Before(out[j].date) })
	return out
}

// classify buckets a release as "album", "eps", or "single". Spotify's own
// album_type only distinguishes "album" from "single"/"compilation" — it has
// no EP concept — so EPs are separated from true singles by track count,
// which is inherently a heuristic; minEP/maxEP are per-widget config so it's
// tunable rather than hardcoded.
func classify(al spotifyapi.Album, minEP, maxEP int) string {
	if al.AlbumType == "album" {
		return "album"
	}
	switch {
	case al.TotalTracks <= 2:
		return "single"
	case al.TotalTracks >= minEP && al.TotalTracks <= maxEP:
		return "eps"
	default:
		return "album" // long single-tagged release (deluxe/maxi single, etc.)
	}
}

// parseReleaseDate parses Spotify's release_date using its accompanying
// precision ("day" | "month" | "year") — a pre-announced future release often
// only carries a year or month.
func parseReleaseDate(date, precision string) (time.Time, bool) {
	layout := "2006-01-02"
	switch precision {
	case "month":
		layout = "2006-01"
	case "year":
		layout = "2006"
	}
	t, err := time.Parse(layout, date)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
