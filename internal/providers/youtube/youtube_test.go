package youtube

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/maxbasque/quarks/internal/core"
)

const channelFeed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns:yt="http://www.youtube.com/xml/schemas/2015"
      xmlns:media="http://search.yahoo.com/mrss/"
      xmlns="http://www.w3.org/2005/Atom">
  <title>Some Channel</title>
  <entry>
    <id>yt:video:vvvvvvvvvvv</id>
    <yt:videoId>vvvvvvvvvv</yt:videoId>
    <title>A video</title>
    <link rel="alternate" href="https://www.youtube.com/watch?v=vvvvvvvvvv"/>
    <published>2026-09-08T10:00:00+00:00</published>
    <media:group>
      <media:thumbnail url="https://i.ytimg.com/vi/vvvvvvvvvv/hqdefault.jpg" width="480" height="360"/>
    </media:group>
  </entry>
</feed>`

func widgetConfig(t *testing.T, src string) core.WidgetConfig {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	wc, err := core.ParseWidget(*doc.Content[0])
	if err != nil {
		t.Fatalf("ParseWidget: %v", err)
	}
	return wc
}

func TestChannelsBecomeFeeds(t *testing.T) {
	var gotChannel, gotPlaylist string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := r.URL.Query().Get("channel_id"); c != "" {
			gotChannel = c
		}
		if p := r.URL.Query().Get("playlist_id"); p != "" {
			gotPlaylist = p
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(channelFeed))
	}))
	defer srv.Close()

	old := feedBase
	feedBase = srv.URL + "/feeds/videos.xml?"
	defer func() { feedBase = old }()

	p, err := New(widgetConfig(t, "type: youtube\ntitle: YT\nchannels: [UCtestchannelid000000]\nplaylists: [PLtestplaylist00000]\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Items) != 2 { // one from the channel feed, one from the playlist feed
		t.Fatalf("want 2 items, got %d", len(got.Items))
	}
	if gotChannel != "UCtestchannelid000000" || gotPlaylist != "PLtestplaylist00000" {
		t.Errorf("requested channel=%q playlist=%q", gotChannel, gotPlaylist)
	}
	if got.Items[0].Thumbnail == "" {
		t.Error("thumbnail not extracted from media:group")
	}
}

func TestRejectsBadIDs(t *testing.T) {
	if _, err := New(widgetConfig(t, "type: youtube\ntitle: YT\nchannels: [not-a-channel]\n")); err == nil {
		t.Error("expected an error for a non-UC channel id")
	}
	if _, err := New(widgetConfig(t, "type: youtube\ntitle: YT\nplaylists: [WL]\n")); err == nil {
		t.Error("expected an error for Watch Later (WL)")
	}
}

func TestRequiresSomething(t *testing.T) {
	if _, err := New(widgetConfig(t, "type: youtube\ntitle: YT\n")); err == nil {
		t.Error("expected an error with no channels or playlists")
	}
}
