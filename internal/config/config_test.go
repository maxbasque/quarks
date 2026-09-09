package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestLoadWithSecretsAndEnv(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	writeFile(t, filepath.Join(dir, "secrets.yaml"), "reddit_home: https://reddit.example/.rss?feed=TOKEN\n", 0o600)
	t.Setenv("QUARKS_TEST_CAL", "https://cal.example/x.ics")

	writeFile(t, cfgPath, `
window:
  columns: 2
widgets:
  - type: rss
    title: Home
    feeds: [ "${secret:reddit_home}" ]
  - type: rss
    title: Cal
    feeds: [ "${QUARKS_TEST_CAL}" ]
`, 0o644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Window.Columns != 2 {
		t.Errorf("columns = %d", cfg.Window.Columns)
	}

	var s struct {
		Feeds []string `yaml:"feeds"`
	}
	if err := cfg.Widgets[0].Decode(&s); err != nil {
		t.Fatal(err)
	}
	if len(s.Feeds) != 1 || s.Feeds[0] != "https://reddit.example/.rss?feed=TOKEN" {
		t.Errorf("secret not substituted: %v", s.Feeds)
	}

	if err := cfg.Widgets[1].Decode(&s); err != nil {
		t.Fatal(err)
	}
	if s.Feeds[0] != "https://cal.example/x.ics" {
		t.Errorf("env var not substituted: %v", s.Feeds)
	}
}

func TestSecretsFileMustNotBeWorldReadable(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, filepath.Join(dir, "secrets.yaml"), "k: v\n", 0o644)
	writeFile(t, cfgPath, "widgets:\n  - type: rss\n    title: X\n    feeds: [http://x]\n", 0o644)

	if _, err := Load(cfgPath); err == nil {
		t.Fatal("expected an error for a 0644 secrets file")
	}
}

func TestLoadWithoutSecretsFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, "widgets:\n  - type: rss\n    title: X\n    feeds: [http://x]\n", 0o644)

	if _, err := Load(cfgPath); err != nil {
		t.Fatalf("a missing secrets file should be fine: %v", err)
	}
}

func TestUnresolvedTokenBecomesEmpty(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, `
widgets:
  - type: rss
    title: X
    source: "${secret:nope}"
    feeds: [http://x]
`, 0o644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var s struct {
		Source string `yaml:"source"`
	}
	_ = cfg.Widgets[0].Decode(&s)
	if s.Source != "" {
		t.Errorf("unresolved token should be empty, got %q", s.Source)
	}
}
