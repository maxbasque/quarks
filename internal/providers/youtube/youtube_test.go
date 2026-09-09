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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("channel_id") != "UCtestchannelid000000" {
			t.Errorf("channel_id = %q", r.URL.Query().Get("channel_id"))
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(channelFeed))
	}))
	defer srv.Close()

	old := feedBase
	feedBase = srv.URL + "/feeds/videos.xml?channel_id="
	defer func() { feedBase = old }()

	p, err := New(widgetConfig(t, "type: youtube\ntitle: YT\nchannels: [UCtestchannelid000000]\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(got.Items))
	}
	it := got.Items[0]
	if it.Source != "Some Channel" {
		t.Errorf("source = %q, want the channel title", it.Source)
	}
	if it.Thumbnail == "" {
		t.Error("thumbnail not extracted from media:group")
	}
}

func TestRejectsNonChannelID(t *testing.T) {
	if _, err := New(widgetConfig(t, "type: youtube\ntitle: YT\nchannels: [not-a-channel]\n")); err == nil {
		t.Error("expected an error for a non-UC channel id")
	}
}

func TestRequiresChannels(t *testing.T) {
	if _, err := New(widgetConfig(t, "type: youtube\ntitle: YT\n")); err == nil {
		t.Error("expected an error with no channels")
	}
}
