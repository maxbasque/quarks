package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/maxbasque/quarks/internal/config"
	"github.com/maxbasque/quarks/internal/core"
	"github.com/maxbasque/quarks/internal/providers/rss"
	"github.com/maxbasque/quarks/internal/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "quarks:", err)
		os.Exit(1)
	}
}

func run() error {
	defaultCfg := filepath.Join(userConfigDir(), "quarks", "config.yaml")
	cfgPath := flag.String("config", defaultCfg, "path to config.yaml")
	addr := flag.String("addr", "127.0.0.1:7373", "listen address")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	registry := core.NewRegistry()
	registry.Register("rss", rss.New)

	cacheDir := filepath.Join(userCacheDir(), "quarks")
	store, err := core.NewStore(cacheDir)
	if err != nil {
		return err
	}

	sched := core.NewScheduler(store, log)
	ttls := make(map[string]time.Duration)
	seen := make(map[string]bool)

	for i, wc := range cfg.Widgets {
		key := widgetKey(i, wc, seen)
		provider, err := registry.Build(wc)
		if err != nil {
			return fmt.Errorf("widget %d (%s): %w", i, wc.Type, err)
		}
		store.Register(key, displayTitle(wc), wc.Column, wc.Type)
		sched.Add(key, wc, provider)
		ttls[key] = wc.TTL
	}

	srv, err := web.NewServer(store, cfg, ttls)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go sched.Run(ctx)

	httpSrv := &http.Server{Addr: *addr, Handler: srv.Routes()}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	log.Info("quarks listening", "addr", "http://"+*addr, "widgets", len(cfg.Widgets), "cache", cacheDir)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func widgetKey(i int, wc core.WidgetConfig, seen map[string]bool) string {
	base := slugRe.ReplaceAllString(strings.ToLower(wc.Title), "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = wc.Type
	}
	key := fmt.Sprintf("%02d-%s", i, base)
	for seen[key] {
		key += "-x"
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

func userConfigDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return d
	}
	return filepath.Join(os.Getenv("HOME"), ".config")
}

func userCacheDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return d
	}
	return filepath.Join(os.Getenv("HOME"), ".cache")
}
