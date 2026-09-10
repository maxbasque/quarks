// Package app wires config, providers, the scheduler, the store and the web
// server into a running dashboard, and reloads the widget set in place when the
// config file changes.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/maxbasque/quarks/internal/config"
	"github.com/maxbasque/quarks/internal/core"
	"github.com/maxbasque/quarks/internal/providers/hackernews"
	"github.com/maxbasque/quarks/internal/providers/nhl"
	"github.com/maxbasque/quarks/internal/providers/reddit"
	"github.com/maxbasque/quarks/internal/providers/rss"
	"github.com/maxbasque/quarks/internal/providers/standings"
	"github.com/maxbasque/quarks/internal/providers/weather"
	"github.com/maxbasque/quarks/internal/providers/youtube"
	"github.com/maxbasque/quarks/internal/web"
)

type App struct {
	cfgPath string
	log     *slog.Logger

	registry *core.Registry
	store    *core.Store
	srv      *web.Server

	mu      sync.Mutex // guards current
	current *runtime
	lastMod time.Time // touched only by Run and watch
}

// runtime is one generation of the scheduler — cancel it and wait on done to
// stop it fully before starting the next.
type runtime struct {
	cancel context.CancelFunc
	done   chan struct{}
	sched  *core.Scheduler
}

func New(cfgPath, cacheDir string, log *slog.Logger) (*App, error) {
	store, err := core.NewStore(cacheDir)
	if err != nil {
		return nil, err
	}

	reg := core.NewRegistry()
	reg.Register("rss", rss.New)
	reg.Register("hackernews", hackernews.New)
	reg.Register("weather", weather.New)
	reg.Register("reddit", reddit.New)
	reg.Register("youtube", youtube.New)
	reg.Register("nhl", nhl.New)
	reg.Register("standings", standings.New)

	a := &App{cfgPath: cfgPath, log: log, registry: reg, store: store}

	srv, err := web.NewServer(store, a.Refresh)
	if err != nil {
		return nil, err
	}
	a.srv = srv
	return a, nil
}

// Refresh fetches widget key (or all, if key is empty) immediately, bypassing
// the schedule. Blocks until done. Returns whether any widget matched.
func (a *App) Refresh(key string) bool {
	a.mu.Lock()
	rt := a.current
	a.mu.Unlock()
	if rt == nil {
		return false
	}
	return rt.sched.Refresh(key)
}

// Run loads the config, starts the HTTP server and the config watcher, and
// blocks until ctx is cancelled.
func (a *App) Run(ctx context.Context, addr string) error {
	if err := a.reload(ctx); err != nil {
		return fmt.Errorf("initial config: %w", err)
	}
	a.lastMod = a.watchStamp()
	go a.watch(ctx)

	httpSrv := &http.Server{Addr: addr, Handler: a.srv.Routes()}
	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shCtx)
	}()

	a.log.Info("quarks listening", "addr", "http://"+addr)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// watch polls the config and secrets files' mtimes and reloads on change. A
// reload that fails to parse is logged and the previous generation keeps running.
func (a *App) watch(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			mod := a.watchStamp()
			if mod == a.lastMod || mod.IsZero() {
				continue
			}
			a.lastMod = mod
			a.log.Info("config changed, reloading", "path", a.cfgPath)
			if err := a.reload(ctx); err != nil {
				a.log.Error("reload failed, keeping previous config", "err", err)
			}
		}
	}
}

// watchStamp is the newest mtime across the config file and the secrets file
// beside it. Zero if the config file is unreadable.
func (a *App) watchStamp() time.Time {
	fi, err := os.Stat(a.cfgPath)
	if err != nil {
		return time.Time{}
	}
	newest := fi.ModTime()
	if si, err := os.Stat(filepath.Join(filepath.Dir(a.cfgPath), "secrets.yaml")); err == nil && si.ModTime().After(newest) {
		newest = si.ModTime()
	}
	return newest
}

func (a *App) reload(ctx context.Context) error {
	cfg, err := config.Load(a.cfgPath)
	if err != nil {
		return err
	}

	sched := core.NewScheduler(a.store, a.log)
	ttls := map[string]time.Duration{}
	keep := map[string]bool{}
	seen := map[string]bool{}
	var pages []web.Page
	order := 0

	for _, pg := range cfg.Pages {
		var boxes []web.Box
		for bi, box := range pg.Boxes {
			var members []string
			for _, wc := range box.Widgets {
				key := widgetKey(wc, seen)
				provider, err := a.registry.Build(wc)
				if err != nil {
					return fmt.Errorf("page %q, box %d (%s): %w", pg.Name, bi, wc.Type, err)
				}
				title := displayTitle(wc)
				a.store.Register(key, title, order, wc.Column, wc.Type)
				sched.Add(key, title, wc, provider)
				ttls[key] = wc.TTL
				keep[key] = true
				members = append(members, key)
				order++
			}
			boxes = append(boxes, web.Box{
				Column: box.Column, Order: bi, Title: box.Title, Members: members,
			})
		}
		pages = append(pages, web.Page{
			Name:          pg.Name,
			Columns:       pg.Columns,
			ColumnWeights: pg.ColumnWeights,
			Boxes:         boxes,
		})
	}
	a.store.Retain(keep)

	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		sched.Run(rctx)
		close(done)
	}()

	a.mu.Lock()
	old := a.current
	a.current = &runtime{cancel: cancel, done: done, sched: sched}
	a.mu.Unlock()
	if old != nil {
		old.cancel()
		select {
		case <-old.done:
		case <-time.After(5 * time.Second):
			a.log.Warn("previous scheduler slow to stop")
		}
	}

	a.srv.Publish(web.Meta{
		Theme: cfg.Window.Theme,
		TTLs:  ttls,
		Pages: pages,
	})
	a.log.Info("config loaded", "pages", len(cfg.Pages), "widgets", len(keep))
	return nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// widgetKey is a stable identifier for a widget instance — the disk-cache
// filename and the store map key. Derived from the title so reordering widgets
// in the config keeps caches intact.
func widgetKey(wc core.WidgetConfig, seen map[string]bool) string {
	base := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(wc.Title), "-"), "-")
	if base == "" {
		base = wc.Type
	}
	key := base
	for n := 2; seen[key]; n++ {
		key = fmt.Sprintf("%s-%d", base, n)
	}
	seen[key] = true
	return key
}

func displayTitle(wc core.WidgetConfig) string {
	if wc.Title != "" {
		return wc.Title
	}
	return strings.ToUpper(wc.Type[:1]) + wc.Type[1:]
}
