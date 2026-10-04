// A thin wrapper over the generic rss provider: it turns channel IDs and
// playlist IDs into YouTube's free, keyless Atom feed URLs
// (youtube.com/feeds/videos.xml?channel_id=… / ?playlist_id=…), which carry
// title, link, published time, author and a media:group thumbnail.
//
// v1 keeps the lists in config. Auto-syncing subscriptions is the M6 OAuth
// milestone (plan §9). Watch Later / Liked are not available from YouTube at
// all — use an unlisted playlist instead.

import { type Provider, Settings, type WidgetConfig } from "../core/provider.ts";
import { RSSProvider } from "./rss/rss.ts";

// feedBase is exported so tests can point it at a local fixture server.
export const youtube = { feedBase: "https://www.youtube.com/feeds/videos.xml?" };

// newYouTube is the core.Factory for "youtube".
export function newYouTube(cfg: WidgetConfig): Provider {
  const s = Settings.of(cfg);
  const channels = s.strList("channels");
  const playlists = s.strList("playlists");
  const title = JSON.stringify(cfg.title);
  if (channels.length === 0 && playlists.length === 0) {
    throw new Error(`youtube widget ${title}: no channels or playlists`);
  }

  const feeds: string[] = [];
  for (let c of channels) {
    c = c.trim();
    if (!c.startsWith("UC")) {
      throw new Error(`youtube widget ${title}: ${JSON.stringify(c)} is not a channel ID (expected UC…)`);
    }
    feeds.push(youtube.feedBase + "channel_id=" + c);
  }
  for (let p of playlists) {
    p = p.trim();
    if (!p.startsWith("PL")) {
      throw new Error(
        `youtube widget ${title}: ${JSON.stringify(p)} is not a playlist ID (expected PL…; ` +
          "Watch Later and Liked can't be fetched — use an unlisted playlist)",
      );
    }
    feeds.push(youtube.feedBase + "playlist_id=" + p);
  }

  // Empty source → the rss provider falls back to each feed's own title (the
  // channel/playlist name). interleave so a busy channel doesn't crowd the
  // others out. No summaries — YouTube descriptions are walls of links.
  return new RSSProvider(feeds, "", true, false);
}
