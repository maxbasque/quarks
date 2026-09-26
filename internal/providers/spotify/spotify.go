// Package spotify surfaces upcoming releases from the widget owner's followed
// Spotify artists — albums and EPs together by default, or either alone. Spotify only supplies who you follow; the releases
// themselves come from MusicBrainz (see shared.go for why). The cache is owned
// by a shared, per-account object so two widget instances reading the same
// account never double the load between them — there's no background
// goroutine; Fetch polls synchronously (gated by a minimum interval),
// triggered by the widget's own TTL or a manual refresh click.
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
	// Include is "all" (default) | "album" | "eps", matched against
	// MusicBrainz's own release-group type. (ep_min_tracks/ep_max_tracks from the Spotify-only
	// days are no longer needed and are ignored if still present.)
	Include string `yaml:"include"`

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
	shared  *shared
	include string
}

// parsed is widgetSettings after defaulting and validation — split out from
// New so parsing can be unit tested without acquire()'s side effect of
// starting a real, network-hitting background poller.
type parsed struct {
	include string
	creds   spotifyapi.Credentials
}

func parseSettings(cfg core.WidgetConfig) (parsed, error) {
	var s widgetSettings
	if err := cfg.Decode(&s); err != nil {
		return parsed{}, err
	}

	include := strings.ToLower(strings.TrimSpace(s.Include))
	if include == "" {
		include = "all"
	}
	if include != "all" && include != "album" && include != "eps" {
		return parsed{}, fmt.Errorf("spotify widget %q: include must be \"all\", \"album\" or \"eps\", got %q", cfg.Title, s.Include)
	}

	creds := spotifyapi.Credentials{ClientID: s.ClientID, ClientSecret: s.ClientSecret, RefreshToken: s.RefreshToken}
	return parsed{include: include, creds: creds}, nil
}

// New is the core.Factory for "spotify". It only fails for structural config
// mistakes (a bad include) — missing credentials produce a
// working Provider whose Fetch reports "not connected" until secrets.yaml has
// them, rather than refusing to build at all.
func New(cfg core.WidgetConfig) (core.Provider, error) {
	p, err := parseSettings(cfg)
	if err != nil {
		return nil, err
	}
	prov := &Provider{include: p.include}
	if p.creds.ClientID != "" && p.creds.ClientSecret != "" && p.creds.RefreshToken != "" {
		prov.shared = acquire(p.creds, slog.Default())
	}
	return prov, nil
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	if p.shared == nil {
		return core.Payload{}, fmt.Errorf("spotify: not connected — connect Spotify from the Settings page")
	}
	if err := p.shared.maybePoll(ctx); err != nil {
		// The store keeps the last good items on error, so this shows as a
		// failure badge over stale data rather than a silently empty list.
		return core.Payload{}, err
	}
	snap := p.shared.snapshot(p.include)
	items := make([]core.Item, 0, len(snap))
	for _, r := range snap {
		it := core.Item{
			ID:      r.rg.ID,
			Title:   r.rg.Title,
			URL:     r.rg.URL(),
			Source:  r.rg.ArtistCredit,
			Summary: summarize(r),
		}
		// Only an exact day gets a countdown ("in 12d"); a month- or
		// year-only date would read as falsely precise, so it's in the
		// summary instead.
		if r.precision == "day" {
			it.PublishedAt = r.date
		}
		items = append(items, it)
	}
	return core.Feed(items), nil
}

// summarize renders e.g. "Album · Live · 13 Oct 2026" or "EP · Nov 2026".
func summarize(r releaseEntry) string {
	parts := []string{"Album"}
	if r.class == "eps" {
		parts[0] = "EP"
	}
	parts = append(parts, r.rg.SecondaryTypes...)
	switch r.precision {
	case "day":
		parts = append(parts, r.date.Format("2 Jan 2006"))
	case "month":
		parts = append(parts, r.date.Format("Jan 2006"))
	case "year":
		parts = append(parts, r.date.Format("2006")+", date TBA")
	}
	return strings.Join(parts, " · ")
}
