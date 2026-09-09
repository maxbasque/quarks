package web

import (
	"embed"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/maxbasque/quarks/internal/core"
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
	store *core.Store
	meta  atomic.Pointer[Meta]
	tmpl  *template.Template
}

func NewServer(store *core.Store) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"since": humanSince,
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{store: store, tmpl: tmpl}
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
	mux.HandleFunc("/", s.handleIndex)
	return mux
}

type widgetVM struct {
	Title  string
	Items  []core.Item
	Badge  string // "", "stale · 14m", "offline"
	Fresh  string // "updated 3m ago" / "never"
	Danger bool
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
		vm := widgetVM{Title: st.Title, Items: st.Items, Fresh: freshLabel(st.LastOK)}
		ttl := meta.TTLs[st.Key]
		switch {
		case len(st.Items) == 0 && st.LastErr != "":
			vm.Badge, vm.Danger = "offline", true
		case st.LastErr != "":
			vm.Badge = "stale · " + humanSince(st.LastOK)
		case ttl > 0 && st.Stale(ttl*2):
			vm.Badge = "stale · " + humanSince(st.LastOK)
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
	if t.IsZero() {
		return "never"
	}
	return "updated " + humanSince(t) + " ago"
}

func humanSince(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	}
}
