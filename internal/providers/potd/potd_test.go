package potd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maxbasque/quarks/internal/core"
)

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

const body = `{"image":{
  "title":"File:Grand Canyon vue.jpg",
  "file_page":"https://commons.wikimedia.org/wiki/File:Grand_Canyon_vue.jpg",
  "thumbnail":{"source":"https://upload.wikimedia.org/wikipedia/commons/thumb/a/ab/Grand_Canyon_vue.jpg/640px-Grand_Canyon_vue.jpg"},
  "artist":{"text":"<a href=\"x\">Jane Doe</a>"},
  "license":{"type":"CC BY-SA 4.0"},
  "description":{"text":"A <b>wide</b> view of the canyon.","lang":"en"},
  "structured":{"captions":{"en":"Wide view of the canyon","fr":"Vue large du canyon"}}
}}`

func TestFetch(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	p, err := New(widgetConfig(t, "type: potd\ntitle: Photo du jour\nlang: fr\n"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pv := p.(*Provider)
	pv.base = srv.URL + "/%s"
	pv.now = func() time.Time { return time.Date(2026, 2, 3, 9, 0, 0, 0, time.UTC) }

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotPath != "/fr/2026/02/03" {
		t.Errorf("path = %q, want /fr/2026/02/03", gotPath)
	}
	if len(got.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(got.Items))
	}
	it := got.Items[0]
	if !it.Hero {
		t.Errorf("Hero should be true")
	}
	if it.Title != "Vue large du canyon" {
		t.Errorf("Title = %q, want the French caption", it.Title)
	}
	if it.Thumbnail != "https://upload.wikimedia.org/wikipedia/commons/thumb/a/ab/Grand_Canyon_vue.jpg/1280px-Grand_Canyon_vue.jpg" {
		t.Errorf("Thumbnail not upscaled: %q", it.Thumbnail)
	}
	if it.URL != "https://commons.wikimedia.org/wiki/File:Grand_Canyon_vue.jpg" {
		t.Errorf("URL = %q", it.URL)
	}
	if it.Author != "Jane Doe · CC BY-SA 4.0" {
		t.Errorf("Author = %q, want artist tags stripped + license", it.Author)
	}
	if it.Summary != "" {
		t.Errorf("Summary = %q, want empty when caption is the title", it.Summary)
	}
	if !it.PublishedAt.IsZero() {
		t.Errorf("PublishedAt should be zero")
	}
}

func TestCaptionFallback(t *testing.T) {
	const enOnly = `{"image":{"title":"File:X.jpg","file_page":"p",
	  "thumbnail":{"source":"https://u/300px-X.jpg"},
	  "description":{"text":"desc","lang":"en"},
	  "structured":{"captions":{"en":"english caption"}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(enOnly))
	}))
	defer srv.Close()

	p, _ := New(widgetConfig(t, "type: potd\ntitle: x\nlang: fr\n"))
	pv := p.(*Provider)
	pv.base = srv.URL + "/%s"
	pv.now = func() time.Time { return time.Date(2026, 2, 3, 9, 0, 0, 0, time.UTC) }

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Items[0].Title != "english caption" {
		t.Errorf("Title = %q, want English fallback", got.Items[0].Title)
	}
}

func TestYesterdayFallback(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if len(paths) == 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	p, _ := New(widgetConfig(t, "type: potd\ntitle: x\n"))
	pv := p.(*Provider)
	pv.base = srv.URL + "/%s"
	pv.now = func() time.Time { return time.Date(2026, 2, 3, 9, 0, 0, 0, time.UTC) }

	got, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(paths) != 2 || paths[1] != "/fr/2026/02/02" {
		t.Errorf("expected a retry on the previous day, got %v", paths)
	}
	if len(got.Items) != 1 {
		t.Fatalf("got %d items", len(got.Items))
	}
}
