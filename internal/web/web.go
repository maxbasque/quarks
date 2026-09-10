package web

import (
	"context"
	"embed"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/maxbasque/quarks/internal/core"
	"github.com/maxbasque/quarks/internal/reader"
)

//go:embed templates/*.html static
var assets embed.FS

// Meta is the config-derived context the renderer needs beyond what the Store
// already carries. The app publishes a new one on every config reload.
type Meta struct {
	Theme string
	TTLs  map[string]time.Duration // widget key -> ttl, for the stale badge
	Pages []Page
}

// Page is one top-level tab: its column layout and cards.
type Page struct {
	Name          string
	Columns       int
	ColumnWeights []float64
	Boxes         []Box
}

// Box is one card. Members are widget keys; more than one means a tabbed card.
type Box struct {
	Column  int
	Order   int
	Title   string
	Members []string
}

// Server renders the dashboard from whatever is currently in the store.
type Server struct {
	store   *core.Store
	reader  *reader.Reader
	refresh func(key string) bool // fetch now, bypassing the schedule
	meta    atomic.Pointer[Meta]
	tmpl    *template.Template
}

func NewServer(store *core.Store, refresh func(key string) bool) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"ago":   ago,
		"temp":  temp,
		"wicon": weatherIcon,
		"day":   func(t time.Time) string { return t.Format("Mon") },
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{store: store, reader: reader.New(), refresh: refresh, tmpl: tmpl}
	s.meta.Store(&Meta{Theme: "dark"})
	return s, nil
}

// Publish swaps in a new config-derived Meta. Safe to call concurrently with
// request handling.
func (s *Server) Publish(m Meta) {
	s.meta.Store(&m)
}

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/static/", noCacheRevalidate(http.FileServer(http.FS(assets))))
	mux.HandleFunc("/manifest.webmanifest", s.handleManifest)
	mux.HandleFunc("/open", s.handleOpen)
	mux.HandleFunc("/refresh", s.handleRefresh)
	mux.HandleFunc("/reader", s.handleReader)
	mux.HandleFunc("/", s.handleIndex)
	return mux
}

// handleOpen hands a URL to the OS default browser. The app-window (Chromium in
// --app mode) would otherwise open links in a second Chromium window; the
// frontend routes clicks here only when it is running standalone.
func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	target := r.FormValue("url")
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, "bad url", http.StatusBadRequest)
		return
	}
	if err := openInBrowser(u.String()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRefresh fetches a widget (?key=…) or all widgets (no key) immediately.
// It blocks until the fetch finishes, so the client can reload right after.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.refresh == nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	if !s.refresh(r.FormValue("key")) {
		http.Error(w, "unknown widget", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	data, err := assets.ReadFile("static/manifest.webmanifest")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

type readerVM struct {
	Theme   string
	Article reader.Article
	Err     string
	URL     string
}

// handleReader renders one extracted article. It works as a standalone page (a
// plain link, no JS) and as a fragment the dashboard pulls in — the markup is the
// same either way; app.js lifts the <article> out.
func (s *Server) handleReader(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("url")
	vm := readerVM{Theme: s.meta.Load().Theme, URL: target}

	if target == "" {
		http.Error(w, "missing url", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	art, err := s.reader.Get(ctx, target)
	if err != nil {
		vm.Err = err.Error()
	} else {
		vm.Article = art
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "reader.html", vm); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type tabVM struct {
	Key       string
	Title     string
	Items     []core.Item
	Weather   *core.Weather
	Standings *core.Standings
	Badge     string // "", "stale · 14m", "offline"
	Fresh     string // "updated 3m ago" / "never updated"
	Danger    bool
}

type boxVM struct {
	Title  string // optional group label
	Tabs   []tabVM
	Tabbed bool
	Danger bool
}

type pageVM struct {
	Name     string
	Slug     string
	GridCols template.CSS // value for grid-template-columns
	Columns  [][]boxVM
}

type indexVM struct {
	Theme     string
	MultiPage bool
	Pages     []pageVM
}

// gridColumns builds the grid-template-columns value from the column count and
// optional per-column weights. The output is derived only from an int and parsed
// floats, so it is safe as trusted CSS.
func gridColumns(n int, weights []float64) template.CSS {
	if len(weights) == n {
		parts := make([]string, n)
		for i, w := range weights {
			if w <= 0 {
				w = 1
			}
			parts[i] = strconv.FormatFloat(w, 'g', -1, 64) + "fr"
		}
		return template.CSS(strings.Join(parts, " "))
	}
	return template.CSS("repeat(" + strconv.Itoa(n) + ", 1fr)")
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	meta := s.meta.Load()

	byKey := make(map[string]core.WidgetState)
	for _, st := range s.store.Snapshot() {
		byKey[st.Key] = st
	}

	vm := indexVM{Theme: meta.Theme, MultiPage: len(meta.Pages) > 1}
	for pi, pg := range meta.Pages {
		n := pg.Columns
		if n < 1 {
			n = 1
		}
		cols := make([][]boxVM, n)

		boxes := append([]Box(nil), pg.Boxes...)
		sort.SliceStable(boxes, func(i, j int) bool { return boxes[i].Order < boxes[j].Order })

		for _, b := range boxes {
			bv := boxVM{Title: b.Title}
			for _, key := range b.Members {
				st := byKey[key]
				tv := tabVM{
					Key:       key,
					Title:     st.Title,
					Items:     st.Items, // already trimmed by the scheduler
					Weather:   st.Weather,
					Standings: st.Standings,
					Fresh:     freshLabel(st.LastOK),
				}

				switch ttl := meta.TTLs[key]; {
				case len(st.Items) == 0 && st.Weather == nil && st.Standings == nil && st.LastErr != "":
					tv.Badge, tv.Danger = "offline", true
				case st.LastErr != "":
					tv.Badge = "stale · " + compactSince(st.LastOK)
				case ttl > 0 && st.Stale(ttl*2):
					tv.Badge = "stale · " + compactSince(st.LastOK)
				}
				bv.Danger = bv.Danger || tv.Danger
				bv.Tabs = append(bv.Tabs, tv)
			}
			bv.Tabbed = len(bv.Tabs) > 1

			ci := b.Column - 1
			if ci < 0 || ci >= n {
				ci = 0
			}
			cols[ci] = append(cols[ci], bv)
		}

		name := pg.Name
		if name == "" {
			name = "Home"
		}
		vm.Pages = append(vm.Pages, pageVM{
			Name:     name,
			Slug:     pageSlug(name, pi),
			GridCols: gridColumns(n, pg.ColumnWeights),
			Columns:  cols,
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if err := s.tmpl.ExecuteTemplate(w, "index.html", vm); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func pageSlug(name string, i int) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if s == "" {
		return "p" + strconv.Itoa(i)
	}
	return s
}

func freshLabel(t time.Time) string {
	switch {
	case t.IsZero():
		return "never updated"
	case time.Since(t) < time.Minute:
		return "updated just now"
	default:
		return "updated " + compactSince(t) + " ago"
	}
}

// ago renders an item timestamp, past or future ("3h ago" / "in 2d"); client JS
// keeps it current after load.
func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	future := d < 0
	if future {
		d = -d
	}
	if future && d < time.Hour {
		return "just now" // small future offset = clock skew, not a real schedule
	}
	var v string
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		v = strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		v = strconv.Itoa(int(d.Hours())) + "h"
	default:
		v = strconv.Itoa(int(d.Hours()/24)) + "d"
	}
	if future {
		return "in " + v
	}
	return v + " ago"
}

// temp rounds a Celsius value to a whole-degree string like "15°".
func temp(c float64) string {
	return strconv.Itoa(int(math.Round(c))) + "°"
}

// weatherIcon maps a WMO code to an emoji.
func weatherIcon(code int) string {
	switch {
	case code == 0:
		return "☀️"
	case code <= 2:
		return "🌤️"
	case code == 3:
		return "☁️"
	case code >= 45 && code <= 48:
		return "🌫️"
	case code >= 51 && code <= 57:
		return "🌦️"
	case code >= 61 && code <= 67:
		return "🌧️"
	case code >= 71 && code <= 77:
		return "🌨️"
	case code >= 80 && code <= 82:
		return "🌧️"
	case code >= 85 && code <= 86:
		return "🌨️"
	case code >= 95:
		return "⛈️"
	default:
		return "•"
	}
}

// compactSince is a short "3m" / "5h" / "2d" duration.
func compactSince(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "0m"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	}
}
