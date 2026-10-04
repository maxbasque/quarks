// Upcoming releases from the widget owner's followed Spotify artists — albums
// and EPs together by default, or either alone. Spotify only supplies who you
// follow; the releases themselves come from MusicBrainz (see shared.ts for
// why). The cache is owned by a shared, per-account object so two widget
// instances reading the same account never double the load between them —
// there's no background loop; fetch polls (gated by a minimum interval),
// triggered by the widget's own TTL or a manual refresh click.

import { type Provider, Settings, type WidgetConfig } from "../../core/provider.ts";
import { feed, type Item, item, type Payload } from "../../core/types.ts";
import * as fr from "../../fr.ts";
import { releaseGroupURL } from "../../musicbrainz.ts";
import type { Credentials } from "../../spotifyapi.ts";
import { acquire, type ReleaseEntry, type Shared } from "./shared.ts";

export interface Parsed {
  include: string; // "all" | "album" | "eps"
  creds: Credentials;
}

// parseSettings is the widget's settings after defaulting and validation —
// split out so parsing can be unit tested without acquire()'s side effects.
export function parseSettings(cfg: WidgetConfig): Parsed {
  const s = Settings.of(cfg);
  // include is matched against MusicBrainz's own release-group type.
  // (ep_min_tracks/ep_max_tracks from the Spotify-only days are no longer
  // needed and are ignored if still present.)
  const raw = s.str("include");
  const include = raw.trim().toLowerCase() || "all";
  if (!["all", "album", "eps"].includes(include)) {
    throw new Error(
      `spotify widget ${JSON.stringify(cfg.title)}: include must be "all", "album" or "eps", got ${
        JSON.stringify(raw)
      }`,
    );
  }
  return {
    include,
    creds: {
      clientId: s.str("client_id"),
      clientSecret: s.str("client_secret"),
      refreshToken: s.str("refresh_token"),
    },
  };
}

// newSpotify is the core.Factory for "spotify". It only fails for structural
// config mistakes (a bad include) — missing credentials produce a working
// Provider whose fetch reports "not connected" until secrets.yaml has them,
// rather than refusing to build at all. Not-yet-connected is an expected,
// transient state, and failing the whole config over it would break the
// app's per-widget failure isolation.
export function newSpotify(cfg: WidgetConfig): Provider {
  const p = parseSettings(cfg);
  const { clientId, clientSecret, refreshToken } = p.creds;
  const shared = clientId && clientSecret && refreshToken ? acquire(p.creds) : null;
  return new SpotifyProvider(shared, p.include);
}

export class SpotifyProvider implements Provider {
  constructor(private shared: Promise<Shared> | Shared | null, private include: string) {}

  async fetch(signal: AbortSignal): Promise<Payload> {
    if (!this.shared) throw new Error("spotify: not connected — connect Spotify from the Settings page");
    const sh = await this.shared;
    const err = await sh.maybePoll(signal);
    // The store keeps the last good items on error, so this shows as a
    // failure badge over stale data rather than a silently empty list.
    if (err) throw err;
    const items: Item[] = sh.snapshot(this.include).map((r) =>
      item({
        id: r.rg.id,
        title: r.rg.title,
        url: releaseGroupURL(r.rg),
        source: r.rg.artistCredit,
        summary: summarize(r),
        // Only an exact day gets a countdown ("in 12d"); a month- or year-only
        // date would read as falsely precise, so it's in the summary instead.
        publishedAt: r.precision === "day" ? r.date : null,
      })
    );
    return feed(items);
  }
}

// summarize renders e.g. "Album · Live · 13 oct. 2026" or "EP · nov. 2026".
export function summarize(r: ReleaseEntry): string {
  const parts = [r.class === "eps" ? "EP" : "Album", ...(r.rg.secondaryTypes ?? [])];
  switch (r.precision) {
    case "day":
      parts.push(fr.dayMonthYear(r.date));
      break;
    case "month":
      parts.push(fr.monthYear(r.date));
      break;
    case "year":
      parts.push(r.date.getFullYear() + ", date à venir");
      break;
  }
  return parts.join(" · ");
}
