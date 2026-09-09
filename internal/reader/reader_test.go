package reader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const page = `<!doctype html><html><head><title>Site</title></head><body>
<header>nav junk</header>
<article>
  <h1>The Headline</h1>
  <p>First real paragraph with enough words to look like body content and survive the readability score threshold.</p>
  <p>Second paragraph, also substantive, discussing the topic at some length so the extractor keeps it.</p>
  <script>window.evil = 1;</script>
  <p onclick="steal()">Third paragraph with an inline handler that must be stripped.</p>
</article>
<footer>footer junk</footer>
</body></html>`

func TestGetExtractsAndSanitizes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	art, err := New().Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	html := string(art.HTML)
	if !strings.Contains(html, "First real paragraph") {
		t.Errorf("body text missing:\n%s", html)
	}
	if strings.Contains(html, "<script") || strings.Contains(html, "window.evil") {
		t.Errorf("script not stripped:\n%s", html)
	}
	if strings.Contains(html, "onclick") {
		t.Errorf("inline handler not stripped:\n%s", html)
	}
}

func TestGetRejectsNonHTTP(t *testing.T) {
	if _, err := New().Get(context.Background(), "file:///etc/passwd"); err == nil {
		t.Error("expected an error for a non-http scheme")
	}
}

func TestGetCaches(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	r := New()
	for i := 0; i < 3; i++ {
		if _, err := r.Get(context.Background(), srv.URL); err != nil {
			t.Fatal(err)
		}
	}
	if hits != 1 {
		t.Errorf("expected 1 upstream hit, got %d", hits)
	}
}
