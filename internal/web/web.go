package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"html/template"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maxbasque/quarks/internal/config"
	"github.com/maxbasque/quarks/internal/core"
	"github.com/maxbasque/quarks/internal/fr"
	"github.com/maxbasque/quarks/internal/reader"
	"github.com/maxbasque/quarks/internal/spotifyapi"
)

//go:embed templates/*.html static
var assets embed.FS

// Meta is the config-derived context the renderer needs beyond what the Store
// already carries. The app publishes a new one on every config reload.
type Meta struct {
	Theme string
	TTLs  map[string]time.Duration // widget key -> ttl, for the stale badge
	Pages []Page                   // shown pages only
	// Layout is every named page, shown or not, with its column choices —
	// what the Settings page offers to toggle.
	Layout []LayoutPage
}

// LayoutPage is one page as Settings sees it.
type LayoutPage struct {
	Name       string
	Enabled    bool
	Columns    []config.ColumnChoice // nil unless the page declares named columns
	MaxColumns int
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
	store       *core.Store
	reader      *reader.Reader
	refresh     func(key string) bool // fetch now, bypassing the schedule
	secretsPath string                // for the settings page to read/write credentials
	layoutPath  string                // for the settings page to save column choices
	reloadNow   func() error          // re-run the app's config.Load -> registry -> scheduler pipeline
	meta        atomic.Pointer[Meta]
	metaGen     atomic.Uint64
	tmpl        *template.Template

	spotify             *spotifyapi.Client
	spotifyPendingState atomic.Pointer[spotifyPending]
	spotifyStatus       atomic.Pointer[spotifyStatus]

	renderMu sync.Mutex
	rendered atomic.Pointer[renderedIndex]
}

// renderedIndex is a cached render of "/" plus the store/meta versions it was
// built from, so repeated polls of an unchanged dashboard cost a version compare.
type renderedIndex struct {
	storeGen, metaGen uint64
	etag              string
	body              []byte
}

func NewServer(store *core.Store, refresh func(key string) bool, secretsPath, layoutPath string, reloadNow func() error, spotify *spotifyapi.Client) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"ago":   ago,
		"temp":  temp,
		"wicon": weatherIcon,
		"day":   fr.Day,
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{
		store: store, reader: reader.New(), refresh: refresh,
		secretsPath: secretsPath, layoutPath: layoutPath, reloadNow: reloadNow, spotify: spotify, tmpl: tmpl,
	}
	s.meta.Store(&Meta{Theme: "dark"})
	return s, nil
}

// Publish swaps in a new config-derived Meta. Safe to call concurrently with
// request handling.
func (s *Server) Publish(m Meta) {
	s.meta.Store(&m)
	s.metaGen.Add(1)
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/static/", noCacheRevalidate(http.FileServer(http.FS(assets))))
	mux.HandleFunc("/manifest.webmanifest", s.handleManifest)
	mux.HandleFunc("/open", s.handleOpen)
	mux.HandleFunc("/refresh", s.handleRefresh)
	mux.HandleFunc("/reader", s.handleReader)
	mux.HandleFunc("/settings", s.handleSettings)
	mux.HandleFunc("/settings/spotify/credentials", s.handleSpotifyCredentials)
	mux.HandleFunc("/settings/spotify/authorize", s.handleSpotifyAuthorize)
	mux.HandleFunc("/settings/spotify/callback", s.handleSpotifyCallback)
	mux.HandleFunc("/settings/spotify/disconnect", s.handleSpotifyDisconnect)
	mux.HandleFunc("/settings/layout", s.handleLayoutSet)
	mux.HandleFunc("/settings/secrets/set", s.handleSecretsSet)
	mux.HandleFunc("/settings/secrets/delete", s.handleSecretsDelete)
	mux.HandleFunc("/", s.handleIndex)
	// The server has no auth — it's only safe because nothing but the user's
	// own browser should reach it. Any web page the user visits can still aim
	// requests at 127.0.0.1, so: refuse cross-site POSTs (a hidden form
	// rewriting secrets.yaml), and refuse Host names other than localhost or
	// an IP literal (DNS rebinding — evil.example resolving to 127.0.0.1 so
	// its scripts can read the dashboard, or use /reader to fetch LAN pages).
	return checkHost(http.NewCrossOriginProtection().Handler(mux))
}

func checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		if host != "localhost" && net.ParseIP(host) == nil {
			http.Error(w, "unrecognized host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleOpen hands a URL to the OS default browser. The native windows
// (quarks-window on Linux, the macOS app) can't open new windows, so their
// injected script sends off-site links here; app.js does the same when the
// page runs as an installed web app.
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
	Badge     string // "", "en retard · 14 min", "hors ligne"
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

	ri := s.indexHTML()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", ri.etag)
	if strings.Contains(r.Header.Get("If-None-Match"), ri.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(ri.body)
}

// indexHTML returns the current rendered "/", rebuilding it only when the store
// or the config has changed since the last render.
func (s *Server) indexHTML() *renderedIndex {
	sg, mg := s.store.Gen(), s.metaGen.Load()
	if ri := s.rendered.Load(); ri != nil && ri.storeGen == sg && ri.metaGen == mg {
		return ri
	}

	s.renderMu.Lock()
	defer s.renderMu.Unlock()

	// read versions again inside the lock, then snapshot, so the render can't be
	// tagged newer than its inputs
	sg, mg = s.store.Gen(), s.metaGen.Load()
	if ri := s.rendered.Load(); ri != nil && ri.storeGen == sg && ri.metaGen == mg {
		return ri
	}

	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, "index.html", s.buildIndexVM()); err != nil {
		return &renderedIndex{etag: `"error"`, body: []byte("render error: " + err.Error())}
	}
	sum := sha256.Sum256(buf.Bytes())
	ri := &renderedIndex{
		storeGen: sg, metaGen: mg,
		etag: fmt.Sprintf(`"%x"`, sum[:12]),
		body: buf.Bytes(),
	}
	s.rendered.Store(ri)
	return ri
}

func (s *Server) buildIndexVM() indexVM {
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
					tv.Badge, tv.Danger = "hors ligne", true
				case st.LastErr != "":
					tv.Badge = "en retard · " + compactSince(st.LastOK)
				case ttl > 0 && st.Stale(ttl*2):
					tv.Badge = "en retard · " + compactSince(st.LastOK)
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
			name = "Accueil"
		}
		vm.Pages = append(vm.Pages, pageVM{
			Name:     name,
			Slug:     pageSlug(name, pi),
			GridCols: gridColumns(n, pg.ColumnWeights),
			Columns:  cols,
		})
	}
	return vm
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
		return "jamais mis à jour"
	case time.Since(t) < time.Minute:
		return "mis à jour à l'instant"
	default:
		return "mis à jour il y a " + compactSince(t)
	}
}

// ago renders an item timestamp, past or future ("il y a 3 h" / "dans 2 j");
// client JS keeps it current after load.
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
		return "à l'instant" // small future offset = clock skew, not a real schedule
	}
	var v string
	switch {
	case d < time.Minute:
		return "à l'instant"
	case d < time.Hour:
		v = strconv.Itoa(int(d.Minutes())) + " min"
	case d < 24*time.Hour:
		v = strconv.Itoa(int(d.Hours())) + " h"
	default:
		v = strconv.Itoa(int(d.Hours()/24)) + " j"
	}
	if future {
		return "dans " + v
	}
	return "il y a " + v
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

// compactSince is a short "3 min" / "5 h" / "2 j" duration.
func compactSince(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "0 min"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " h"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + " j"
	}
}
