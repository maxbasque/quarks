package core

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// job binds a live provider to its widget key and TTL.
type job struct {
	key      string
	ttl      time.Duration
	limit    int
	provider Provider
}

// Scheduler runs one loop per widget, each on its own TTL, writing results into
// the Store. A failing widget never affects the others.
type Scheduler struct {
	store *Store
	log   *slog.Logger
	jobs  []job
}

func NewScheduler(store *Store, log *slog.Logger) *Scheduler {
	return &Scheduler{store: store, log: log}
}

// Add registers a widget instance. key must be unique and stable across restarts
// (it is the disk-cache filename).
func (s *Scheduler) Add(key string, cfg WidgetConfig, p Provider) {
	s.jobs = append(s.jobs, job{key: key, ttl: cfg.TTL, limit: cfg.Limit, provider: p})
}

// Run starts every widget loop and blocks until ctx is cancelled and every loop
// has returned. That lets a caller (config reload) know the old scheduler is
// fully stopped before starting a replacement.
func (s *Scheduler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, j := range s.jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			s.loop(ctx, j)
		}(j)
	}
	<-ctx.Done()
	wg.Wait()
}

func (s *Scheduler) loop(ctx context.Context, j job) {
	s.fetch(ctx, j) // once immediately

	t := time.NewTicker(j.ttl)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.fetch(ctx, j)
		}
	}
}

func (s *Scheduler) fetch(ctx context.Context, j job) {
	fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	payload, err := j.provider.Fetch(fctx)
	if err != nil {
		if ctx.Err() != nil {
			return // shutting down or reloading — not a real feed failure
		}
		s.log.Warn("widget fetch failed", "widget", j.key, "err", err)
		s.store.SetError(j.key, err)
		return
	}
	if j.limit > 0 && len(payload.Items) > j.limit {
		payload.Items = payload.Items[:j.limit]
	}
	s.log.Info("widget refreshed", "widget", j.key, "items", len(payload.Items))
	s.store.SetPayload(j.key, payload)
}
