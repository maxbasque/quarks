// Package spotify surfaces upcoming releases from the widget owner's followed
// Spotify artists, split into an "album" view and an "eps" view of the same
// underlying data. The actual polling is owned by a shared, per-account
// background poller (see shared.go) so that two widget instances reading the
// same account never double the API load between them; Fetch itself never
// hits the network.
package spotify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/maxbasque/quarks/internal/core"
	"github.com/maxbasque/quarks/internal/spotifyapi"
)

type widgetSettings struct {
	Include     string `yaml:"include"`       // "album" | "eps"
	EPMinTracks int    `yaml:"ep_min_tracks"` // default 3
	EPMaxTracks int    `yaml:"ep_max_tracks"` // default 7

	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	RefreshToken string `yaml:"refresh_token"`
}

// Provider is the core.Provider for "spotify". It holds no network state of
// its own — it just filters/reads the shared account-level cache. shared is
// nil until credentials are actually present (see New) — deliberately not a
// Build-time error: not-yet-connected is an expected, transient state (the
// user hasn't clicked Connect yet, or disconnected), and config.Load failing
// the *entire* page over it would break this app's per-widget failure
// isolation. Fetch reports it as an ordinary per-widget error instead, same
// as any other widget whose credentials don't work yet.
type Provider struct {
	shared       *shared
	include      string
	minEP, maxEP int
}

// parsed is widgetSettings after defaulting and validation — split out from
// New so parsing can be unit tested without acquire()'s side effect of
// starting a real, network-hitting background poller.
type parsed struct {
	include      string
	minEP, maxEP int
	creds        spotifyapi.Credentials
}

func parseSettings(cfg core.WidgetConfig) (parsed, error) {
	var s widgetSettings
	if err := cfg.Decode(&s); err != nil {
		return parsed{}, err
	}

	include := strings.ToLower(strings.TrimSpace(s.Include))
	if include == "" {
		include = "album"
	}
	if include != "album" && include != "eps" {
		return parsed{}, fmt.Errorf("spotify widget %q: include must be \"album\" or \"eps\", got %q", cfg.Title, s.Include)
	}

	minEP, maxEP := s.EPMinTracks, s.EPMaxTracks
	if minEP == 0 {
		minEP = 3
	}
	if maxEP == 0 {
		maxEP = 7
	}
	if minEP > maxEP {
		return parsed{}, fmt.Errorf("spotify widget %q: ep_min_tracks (%d) > ep_max_tracks (%d)", cfg.Title, minEP, maxEP)
	}

	creds := spotifyapi.Credentials{ClientID: s.ClientID, ClientSecret: s.ClientSecret, RefreshToken: s.RefreshToken}
	return parsed{include: include, minEP: minEP, maxEP: maxEP, creds: creds}, nil
}

// New is the core.Factory for "spotify". It only fails for structural config
// mistakes (bad include, inverted thresholds) — missing credentials produce a
// working Provider whose Fetch reports "not connected" until secrets.yaml has
// them, rather than refusing to build at all.
func New(cfg core.WidgetConfig) (core.Provider, error) {
	p, err := parseSettings(cfg)
	if err != nil {
		return nil, err
	}
	prov := &Provider{include: p.include, minEP: p.minEP, maxEP: p.maxEP}
	if p.creds.ClientID != "" && p.creds.ClientSecret != "" && p.creds.RefreshToken != "" {
		prov.shared = acquire(p.creds, slog.Default())
	}
	return prov, nil
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	if p.shared == nil {
		return core.Payload{}, fmt.Errorf("spotify: not connected — connect Spotify from the Settings page")
	}
	snap := p.shared.snapshot(p.include, p.minEP, p.maxEP)
	items := make([]core.Item, 0, len(snap))
	for _, r := range snap {
		items = append(items, core.Item{
			ID:          r.album.ID,
			Title:       r.album.Name,
			URL:         r.album.URL,
			Source:      r.album.ArtistName,
			Thumbnail:   r.album.ImageURL,
			PublishedAt: r.date,
			Summary:     summarize(r.class, r.album.TotalTracks),
		})
	}
	return core.Feed(items), nil
}

func summarize(class string, tracks int) string {
	label := "Album"
	if class == "eps" {
		label = "EP"
	}
	if tracks == 1 {
		return label + " · 1 track"
	}
	return fmt.Sprintf("%s · %d tracks", label, tracks)
}
