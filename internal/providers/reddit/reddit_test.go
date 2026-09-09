package reddit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/maxbasque/quarks/internal/core"
)

const sample = `{
  "kind": "Listing",
  "data": {"children": [
    {"kind": "t3", "data": {
      "id": "aaa", "title": "A link post", "url": "https://example.com/article",
      "permalink": "/r/selfhosted/comments/aaa/a_link_post/", "score": 128,
      "num_comments": 44, "created_utc": 1788800000, "author": "alice",
      "subreddit_name_prefixed": "r/selfhosted", "thumbnail": "https://b.thumbs.redditmedia.com/x.jpg",
      "is_self": false, "stickied": false
    }},
    {"kind": "t3", "data": {
      "id": "bbb", "title": "A self post", "url": "https://www.reddit.com/r/selfhosted/comments/bbb/",
      "permalink": "/r/selfhosted/comments/bbb/a_self_post/", "score": 12, "num_comments": 3,
      "created_utc": 1788790000, "author": "bob", "subreddit_name_prefixed": "r/selfhosted",
      "thumbnail": "self", "is_self": true, "stickied": false
    }},
    {"kind": "t3", "data": {"id": "ccc", "title": "Pinned", "stickied": true, "permalink": "/x"}}
  ]}
}`

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

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/r/selfhosted+bazzite/hot.json" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sample))
	}))
	defer srv.Close()

	p, err := New(widgetConfig(t, "type: reddit\ntitle: Reddit\nsubreddits: [selfhosted, bazzite]\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.(*Provider).base = srv.URL

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	items := got.Items
	if len(items) != 2 {
		t.Fatalf("want 2 items (stickied dropped), got %d", len(items))
	}

	link := items[0]
	if link.URL != "https://example.com/article" {
		t.Errorf("link post URL = %q", link.URL)
	}
	if link.CommentsURL != "https://www.reddit.com/r/selfhosted/comments/aaa/a_link_post/" {
		t.Errorf("CommentsURL = %q", link.CommentsURL)
	}
	if link.Score != 128 || link.Comments != 44 {
		t.Errorf("score/comments = %d/%d", link.Score, link.Comments)
	}
	if link.Author != "u/alice" || link.Source != "r/selfhosted" {
		t.Errorf("author/source = %q/%q", link.Author, link.Source)
	}
	if link.Thumbnail == "" {
		t.Error("link post should keep its http thumbnail")
	}

	self := items[1]
	if self.URL != self.CommentsURL {
		t.Errorf("self post URL should be the permalink, got %q", self.URL)
	}
	if self.Thumbnail != "" {
		t.Errorf("self post should have no thumbnail, got %q", self.Thumbnail)
	}
}

func TestNonJSONResponseIsAClearError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>blocked</html>"))
	}))
	defer srv.Close()

	p, _ := New(widgetConfig(t, "type: reddit\ntitle: R\nsubreddits: [selfhosted]\n"))
	p.(*Provider).base = srv.URL

	_, err := p.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected an error for a non-JSON response")
	}
}

func TestRequiresSubreddits(t *testing.T) {
	if _, err := New(widgetConfig(t, "type: reddit\ntitle: R\n")); err == nil {
		t.Error("expected an error with no subreddits")
	}
}
