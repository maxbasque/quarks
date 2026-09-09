package rss_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/maxbasque/quarks/internal/core"
	"github.com/maxbasque/quarks/internal/providers/rss"
)

// widgetConfig builds a core.WidgetConfig the same way config loading does: parse
// a YAML mapping node through core.ParseWidget.
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

func fetch(t *testing.T, cfgSrc string) []core.Item {
	t.Helper()
	p, err := rss.New(widgetConfig(t, cfgSrc))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	payload, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return payload.Items
}

func TestYouTubeFeed(t *testing.T) {
	srv := httptest.NewServer(http.FileServer(http.Dir("testdata")))
	defer srv.Close()

	items := fetch(t, fmt.Sprintf("type: rss\ntitle: YT\nfeeds: [%s/youtube.xml]\n", srv.URL))

	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}

	first := items[0]
	if first.Title != "First video: nested media:group thumbnail" {
		t.Errorf("title = %q", first.Title)
	}
	if first.Source != "YT" {
		t.Errorf("source = %q, want the widget title", first.Source)
	}
	// thumbnail lives in <media:group><media:thumbnail> — the YouTube nesting
	if first.Thumbnail != "https://i.ytimg.com/vi/aaaaaaaaaaa/hqdefault.jpg" {
		t.Errorf("thumbnail = %q", first.Thumbnail)
	}
	if first.PublishedAt.IsZero() {
		t.Error("first item has zero PublishedAt")
	}
	// newest first
	if items[1].PublishedAt.After(first.PublishedAt) {
		t.Error("items not sorted newest-first")
	}
	if items[1].Thumbnail == "" {
		t.Error("second item lost its bare media:thumbnail")
	}
}

func TestNewsFeed(t *testing.T) {
	srv := httptest.NewServer(http.FileServer(http.Dir("testdata")))
	defer srv.Close()

	items := fetch(t, fmt.Sprintf("type: rss\ntitle: News\nfeeds: [%s/news.xml]\n", srv.URL))

	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}

	byTitle := map[string]core.Item{}
	for _, it := range items {
		byTitle[it.Title] = it
	}

	if got := byTitle["Council approves budget"].Author; got != "Priya Nair" {
		t.Errorf("dc:creator not mapped to Author: %q", got)
	}
	if got := byTitle["Storm warning issued & extended"]; got.Title == "" {
		t.Error("ampersand entity not decoded in title")
	}
	if got := byTitle["Item with no date and no author"]; !got.PublishedAt.IsZero() {
		t.Errorf("missing pubDate should leave PublishedAt zero, got %v", got.PublishedAt)
	}
	for _, it := range items {
		if it.ID == "" || it.URL == "" {
			t.Errorf("item %q missing ID or URL", it.Title)
		}
	}
}

func TestErrorWhenAllFeedsFail(t *testing.T) {
	p, err := rss.New(widgetConfig(t, "type: rss\ntitle: Dead\nfeeds: [http://127.0.0.1:1/nope.xml]\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Fetch(context.Background()); err == nil {
		t.Error("expected an error when every feed is unreachable")
	}
}

func TestRedditHomeFeedShowsSubreddit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">
		  <title>home feed</title>
		  <entry>
		    <id>t3_a</id><title>A post</title>
		    <category term="selfhosted" label="r/selfhosted"/>
		    <link href="https://www.reddit.com/r/selfhosted/comments/a/a_post/"/>
		    <updated>2026-09-08T12:00:00Z</updated>
		  </entry>
		</feed>`)
	}))
	defer srv.Close()

	items := fetch(t, fmt.Sprintf("type: rss\ntitle: Reddit\nfeeds: [%s]\n", srv.URL))
	if len(items) != 1 || items[0].Source != "r/selfhosted" {
		t.Errorf("want Source r/selfhosted, got %+v", items)
	}
}

func TestNonRedditFeedKeepsItsSource(t *testing.T) {
	srv := httptest.NewServer(http.FileServer(http.Dir("testdata")))
	defer srv.Close()
	items := fetch(t, fmt.Sprintf("type: rss\ntitle: News\nfeeds: [%s/news.xml]\n", srv.URL))
	for _, it := range items {
		if it.Source != "News" {
			t.Errorf("non-reddit item source = %q, want the widget title", it.Source)
		}
	}
}

func TestNoFeedsConfigured(t *testing.T) {
	if _, err := rss.New(widgetConfig(t, "type: rss\ntitle: Empty\n")); err == nil {
		t.Error("expected an error when no feeds are configured")
	}
}

func TestInterleaveBalancesBusyFeeds(t *testing.T) {
	// feed A: 5 recent items; feed B: 1 old item.
	mux := http.NewServeMux()
	mux.HandleFunc("/a.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<rss version="2.0"><channel><title>A</title>`+
			item("a1", "2026-09-08T12:00:00Z")+item("a2", "2026-09-08T11:00:00Z")+
			item("a3", "2026-09-08T10:00:00Z")+item("a4", "2026-09-08T09:00:00Z")+
			item("a5", "2026-09-08T08:00:00Z")+`</channel></rss>`)
	})
	mux.HandleFunc("/b.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<rss version="2.0"><channel><title>B</title>`+
			item("b1", "2026-09-01T00:00:00Z")+`</channel></rss>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := fmt.Sprintf("type: rss\ntitle: Mix\ninterleave: true\nfeeds: [%s/a.xml, %s/b.xml]\n", srv.URL, srv.URL)
	items := fetch(t, cfg)

	if len(items) != 6 {
		t.Fatalf("want 6 items, got %d", len(items))
	}
	// round-robin: a1, b1, a2, a3, a4, a5 — B's single item is 2nd, not last.
	if items[1].Title != "b1" {
		t.Errorf("interleave order = %v; want b1 second", titles(items))
	}
}

func item(title, pub string) string {
	return fmt.Sprintf(`<item><title>%s</title><link>https://x/%s</link><guid>%s</guid><pubDate>%s</pubDate></item>`,
		title, title, title, pub)
}

func titles(items []core.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}
