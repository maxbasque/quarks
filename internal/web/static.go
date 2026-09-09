package web

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
)

// staticETag is one content hash over the whole embedded static tree. The files
// only change on a rebuild, and they change together, so a single shared ETag is
// enough for the browser to know when to drop its cache.
var staticETag = func() string {
	h := sha256.New()
	_ = fs.WalkDir(assets, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := assets.ReadFile(p)
		_, _ = h.Write([]byte(p))
		_, _ = h.Write(b)
		return nil
	})
	return fmt.Sprintf(`"%x"`, h.Sum(nil)[:12])
}()

// noCacheRevalidate makes the browser revalidate static assets on every load
// (cheap: a 304 when the ETag matches) instead of serving a stale copy after a
// CSS/JS change.
func noCacheRevalidate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", staticETag)
		next.ServeHTTP(w, r)
	})
}
