package spotify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/maxbasque/quarks/internal/spotifyapi"
)

// pollInterval is how often the shared poller checks a slice of followed
// artists. Small and frequent so a large follow list can be spread thin
// rather than swept all at once, which is what would risk a rate limit.
const pollInterval = 5 * time.Minute

// rotationWindow is the target time for every followed artist to get checked
// at least once. Release announcements don't need checking more often than
// this, so this — not the widget's own TTL — is what actually governs
// Spotify API call volume.
const rotationWindow = 24 * time.Hour

// artistListTTL is how long the followed-artist list itself is trusted before
// a refetch — cheap (a handful of paginated calls) but no reason to redo it
// every tick.
const artistListTTL = 24 * time.Hour

var ticksPerRotation = int(rotationWindow / pollInterval)

// rotationSize returns how many artists to check on one tick so that a full
// pass over total artists completes in roughly one rotationWindow.
func rotationSize(total int) int {
	if total <= 0 {
		return 0
	}
	n := int(math.Ceil(float64(total) / float64(ticksPerRotation)))
	if n < 1 {
		n = 1
	}
	return n
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

// shared is one Spotify account's background release poller — one per
// distinct refresh token, not one per widget instance. Two widgets (an Albums
// tab and an EPs tab) reading the same account converge on the same *shared
// via acquire, so they never double the API load between them. It survives
// config reloads as long as the refresh token doesn't change: acquire()
// re-finds the same instance by credential fingerprint, and startOnce makes
// re-acquiring a no-op rather than restarting the poller. Only a genuine
// reconnect (a new refresh token) creates a new one and abandons the old
// goroutine+cache — an accepted, small leak for a single-user desktop app,
// not worth a shutdown hook on core.Provider for this one case.
type shared struct {
	client *spotifyapi.Client
	creds  spotifyapi.Credentials
	log    *slog.Logger

	tokMu  sync.Mutex
	tok    string
	tokExp time.Time

	mu               sync.RWMutex
	releases         map[string]releaseEntry // spotify album ID -> raw facts
	artists          []spotifyapi.Artist
	artistsCheckedAt time.Time
	cursor           int

	startOnce sync.Once
}

var (
	sharedMu sync.Mutex
	shareds  = map[string]*shared{}
)

// acquire returns the shared poller for creds, creating and starting it on
// first use. The map key is a hash of the refresh token, never the token
// itself, so it's safe to keep in memory/logs.
func acquire(creds spotifyapi.Credentials, log *slog.Logger) *shared {
	key := cacheKey(creds.RefreshToken)

	sharedMu.Lock()
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
	sharedMu.Unlock()

	sh.startOnce.Do(func() { go sh.run() })
	return sh
}

func cacheKey(refreshToken string) string {
	sum := sha256.Sum256([]byte(refreshToken))
	return hex.EncodeToString(sum[:])
}

func (s *shared) run() {
	ctx := context.Background()
	s.pollSlice(ctx) // don't wait a full tick for the first data
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for range t.C {
		s.pollSlice(ctx)
	}
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
// ("album" | "eps" | "single"), sorted soonest-first. No network call — the
// background poller is what keeps the cache current.
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
