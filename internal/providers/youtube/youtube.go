// Package youtube is a thin wrapper over the generic rss provider: it turns
// channel IDs and playlist IDs into YouTube's free, keyless Atom feed URLs
// (youtube.com/feeds/videos.xml?channel_id=… / ?playlist_id=…), which carry
// title, link, published time, author and a media:group thumbnail.
//
// v1 keeps the lists in config. Auto-syncing subscriptions is the M6 OAuth
// milestone (plan §9). Watch Later / Liked are not available from YouTube at all
// — use an unlisted playlist instead.
package youtube

import (
	"fmt"
	"strings"

	"github.com/maxbasque/quarks/internal/core"
	"github.com/maxbasque/quarks/internal/providers/rss"
)

// feedBase is a var so tests can point it at a local fixture server.
var feedBase = "https://www.youtube.com/feeds/videos.xml?"

type settings struct {
	Channels  []string `yaml:"channels"`
	Playlists []string `yaml:"playlists"`
}

// New is the core.Factory for "youtube".
func New(cfg core.WidgetConfig) (core.Provider, error) {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return nil, err
	}
	if len(s.Channels) == 0 && len(s.Playlists) == 0 {
		return nil, fmt.Errorf("youtube widget %q: no channels or playlists", cfg.Title)
	}

	var feeds []string
	for _, c := range s.Channels {
		c = strings.TrimSpace(c)
		if !strings.HasPrefix(c, "UC") {
			return nil, fmt.Errorf("youtube widget %q: %q is not a channel ID (expected UC…)", cfg.Title, c)
		}
		feeds = append(feeds, feedBase+"channel_id="+c)
	}
	for _, p := range s.Playlists {
		p = strings.TrimSpace(p)
		if !strings.HasPrefix(p, "PL") {
			return nil, fmt.Errorf("youtube widget %q: %q is not a playlist ID (expected PL…; "+
				"Watch Later and Liked can't be fetched — use an unlisted playlist)", cfg.Title, p)
		}
		feeds = append(feeds, feedBase+"playlist_id="+p)
	}

	// Empty source → the rss provider falls back to each feed's own title (the
	// channel/playlist name). interleave=true so a busy channel doesn't crowd the
	// others out.
	return rss.NewWithFeeds(feeds, "", true), nil
}
