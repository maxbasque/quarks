package web

import (
	"context"
	"embed"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/maxbasque/quarks/internal/core"
	"github.com/maxbasque/quarks/internal/reader"
)

//go:embed templates/*.html static/*
var assets embed.FS

// Meta is the config-derived context the renderer needs beyond what the Store
// already carries. The app publishes a new one on every config reload.
type Meta struct {
	Columns int
	Theme   string
	TTLs    map[string]time.Duration // widget key -> ttl, for the stale badge
}

// Server renders the dashboard from whatever is currently in the store.
type Server struct {
	store  *core.Store
	reader *reader.Reader
	meta   atomic.Pointer[Meta]
	tmpl   *template.Template
}

func NewServer(store *core.Store) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"ago":   ago,
		"temp":  temp,
		"wicon": weatherIcon,
		"day":   func(t time.Time) string { return t.Format("Mon") },
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{store: store, reader: reader.New(), tmpl: tmpl}
	s.meta.Store(&Meta{Columns: 3, Theme: "dark"})
	return s, nil
}

// Publish swaps in a new config-derived Meta. Safe to call concurrently with
// request handling.
func (s *Server) Publish(m Meta) {
	s.meta.Store(&m)
}

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/static/", http.FileServer(http.FS(assets)))
	mux.HandleFunc("/manifest.webmanifest", s.handleManifest)
	mux.HandleFunc("/open", s.handleOpen)
	mux.HandleFunc("/reader", s.handleReader)
	mux.HandleFunc("/", s.handleIndex)
	return mux
}

// handleOpen hands a URL to the OS default browser via xdg-open. The app-window
// (Chromium in --app mode) would otherwise open links in a second Chromium
// window; the frontend routes clicks here only when it is running standalone.
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
	if err := exec.Command("xdg-open", u.String()).Start(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

type widgetVM struct {
	Title   string
	Items   []core.Item
	Weather *core.Weather
	Badge   string // "", "stale · 14m", "offline"
	Fresh   string // "updated 3m ago" / "never"
	Danger  bool
}

type pageVM struct {
	Theme   string
	Columns [][]widgetVM
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	meta := s.meta.Load()
	states := s.store.Snapshot()
	sort.Slice(states, func(i, j int) bool {
		if states[i].Order != states[j].Order {
			return states[i].Order < states[j].Order
		}
		return states[i].Key < states[j].Key
	})

	n := meta.Columns
	if n < 1 {
		n = 1
	}
	cols := make([][]widgetVM, n)
	for _, st := range states {
		items := make([]core.Item, len(st.Items))
		copy(items, st.Items)
		for i := range items {
			if strings.EqualFold(items[i].Source, st.Title) {
				items[i].Source = "" // redundant with the widget header
			}
			if strings.EqualFold(items[i].Author, items[i].Source) {
				items[i].Author = "" // e.g. a YouTube channel is both
			}
		}

		vm := widgetVM{Title: st.Title, Items: items, Weather: st.Weather, Fresh: freshLabel(st.LastOK)}
		ttl := meta.TTLs[st.Key]
		switch {
		case len(st.Items) == 0 && st.Weather == nil && st.LastErr != "":
			vm.Badge, vm.Danger = "offline", true
		case st.LastErr != "":
			vm.Badge = "stale · " + compactSince(st.LastOK)
		case ttl > 0 && st.Stale(ttl*2):
			vm.Badge = "stale · " + compactSince(st.LastOK)
		}

		ci := st.Column - 1
		if ci < 0 || ci >= n {
			ci = 0
		}
		cols[ci] = append(cols[ci], vm)
	}

	page := pageVM{Theme: meta.Theme, Columns: cols}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "index.html", page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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

// ago renders an item timestamp; client JS keeps it current after load.
func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	if time.Since(t) < time.Minute {
		return "just now"
	}
	return compactSince(t) + " ago"
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
