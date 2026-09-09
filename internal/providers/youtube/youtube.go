// Package youtube is a thin wrapper over the generic rss provider: it turns a
// list of channel IDs into per-channel Atom feed URLs
// (youtube.com/feeds/videos.xml?channel_id=…), which are free, keyless and
// carry title, link, published time, author and a media:group thumbnail.
//
// v1 keeps the channel list in config. Auto-syncing it from your subscriptions
// is the M6 OAuth milestone (plan §9).
package youtube

import (
	"fmt"
	"strings"

	"github.com/maxbasque/quarks/internal/core"
	"github.com/maxbasque/quarks/internal/providers/rss"
)

// feedBase is a var so tests can point it at a local fixture server.
var feedBase = "https://www.youtube.com/feeds/videos.xml?channel_id="

type settings struct {
	Channels []string `yaml:"channels"`
}

// New is the core.Factory for "youtube".
func New(cfg core.WidgetConfig) (core.Provider, error) {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return nil, err
	}
	if len(s.Channels) == 0 {
		return nil, fmt.Errorf("youtube widget %q: no channels", cfg.Title)
	}

	feeds := make([]string, 0, len(s.Channels))
	for _, c := range s.Channels {
		c = strings.TrimSpace(c)
		if !strings.HasPrefix(c, "UC") {
			return nil, fmt.Errorf("youtube widget %q: %q is not a channel ID (expected UC…)", cfg.Title, c)
		}
		feeds = append(feeds, feedBase+c)
	}

	// Empty source → the rss provider falls back to each feed's own title, i.e.
	// the channel name. interleave=true so a channel that uploads often doesn't
	// crowd the others out of the list.
	return rss.NewWithFeeds(feeds, "", true), nil
}
