package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/maxbasque/quarks/internal/app"
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

	cacheDir := filepath.Join(userCacheDir(), "quarks")
	a, err := app.New(*cfgPath, cacheDir, log)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return a.Run(ctx, *addr)
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
