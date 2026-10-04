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

// feeds decodes the "feeds" list of the single widget in box bi of page 0.
func feeds(t *testing.T, cfg *Config, bi int) []string {
	t.Helper()
	var s struct {
		Feeds []string `yaml:"feeds"`
	}
	if err := cfg.Pages[0].Boxes[bi].Widgets[0].Decode(&s); err != nil {
		t.Fatal(err)
	}
	return s.Feeds
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
	if cfg.Pages[0].Columns != 2 {
		t.Errorf("columns = %d", cfg.Pages[0].Columns)
	}

	if got := feeds(t, cfg, 0); len(got) != 1 || got[0] != "https://reddit.example/.rss?feed=TOKEN" {
		t.Errorf("secret not substituted: %v", got)
	}
	if got := feeds(t, cfg, 1); got[0] != "https://cal.example/x.ics" {
		t.Errorf("env var not substituted: %v", got)
	}
}

func TestGroupBecomesOneBoxWithTabs(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, `
widgets:
  - type: hackernews
    column: 1
    title: HN
  - type: group
    column: 2
    title: News
    tabs:
      - { type: rss, title: A, feeds: [http://a] }
      - { type: rss, title: B, feeds: [http://b] }
`, 0o644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	boxes := cfg.Pages[0].Boxes
	if len(boxes) != 2 {
		t.Fatalf("want 2 boxes, got %d", len(boxes))
	}
	if len(boxes[0].Widgets) != 1 {
		t.Errorf("plain widget box should have 1 widget")
	}
	g := boxes[1]
	if g.Title != "News" || len(g.Widgets) != 2 {
		t.Fatalf("group box = %q with %d widgets", g.Title, len(g.Widgets))
	}
	if g.Column != 2 || g.Widgets[0].Column != 2 || g.Widgets[1].Column != 2 {
		t.Errorf("tabs should inherit the group column: %d / %d / %d",
			g.Column, g.Widgets[0].Column, g.Widgets[1].Column)
	}
	if g.Widgets[0].Title != "A" || g.Widgets[1].Title != "B" {
		t.Errorf("tab titles = %q, %q", g.Widgets[0].Title, g.Widgets[1].Title)
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
	_ = cfg.Pages[0].Boxes[0].Widgets[0].Decode(&s)
	if s.Source != "" {
		t.Errorf("unresolved token should be empty, got %q", s.Source)
	}
}

const namedColumnsConfig = `
pages:
  - name: Accueil
    max_columns: 2
    columns:
      - name: Niches
        enabled: false
        weight: 27
        widgets:
          - { type: rss, title: Reddit, feeds: [ "${secret:reddit_home}" ] }
      - name: Nouvelles
        weight: 46
        widgets:
          - { type: rss, title: RC, feeds: [https://rc.example/rss] }
      - name: Aujourd'hui
        weight: 27
        widgets:
          - { type: weather, title: Météo }
          - { type: potd, title: Photo }
`

func TestLoadNamedColumns(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, namedColumnsConfig, 0o644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pg := cfg.Pages[0]
	if pg.Columns != 2 || pg.MaxColumns != 2 {
		t.Errorf("columns = %d, max = %d", pg.Columns, pg.MaxColumns)
	}
	if len(pg.ColumnWeights) != 2 || pg.ColumnWeights[0] != 46 || pg.ColumnWeights[1] != 27 {
		t.Errorf("weights = %v", pg.ColumnWeights)
	}
	if len(pg.Boxes) != 3 {
		t.Fatalf("boxes = %d, want 3 (disabled column's widgets dropped)", len(pg.Boxes))
	}
	if pg.Boxes[0].Column != 1 || pg.Boxes[1].Column != 2 || pg.Boxes[2].Column != 2 {
		t.Errorf("box columns = %d %d %d", pg.Boxes[0].Column, pg.Boxes[1].Column, pg.Boxes[2].Column)
	}
	if pg.Boxes[2].Widgets[0].Column != 2 {
		t.Errorf("widget column = %d", pg.Boxes[2].Widgets[0].Column)
	}

	want := []ColumnChoice{
		{Name: "Niches", MissingSecrets: []string{"reddit_home"}},
		{Name: "Nouvelles", Enabled: true},
		{Name: "Aujourd'hui", Enabled: true},
	}
	if len(pg.Choices) != len(want) {
		t.Fatalf("choices = %+v", pg.Choices)
	}
	for i, c := range pg.Choices {
		if c.Name != want[i].Name || c.Enabled != want[i].Enabled || len(c.MissingSecrets) != len(want[i].MissingSecrets) {
			t.Errorf("choice %d = %+v, want %+v", i, c, want[i])
		}
	}
}

func TestLoadNamedColumnsLayoutOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeFile(t, cfgPath, namedColumnsConfig, 0o644)
	writeFile(t, filepath.Join(dir, "secrets.yaml"), "reddit_home: https://reddit.example/.rss\n", 0o600)
	if err := SetPageColumns(LayoutPath(cfgPath), "Accueil", []string{"Niches", "Nouvelles"}); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pg := cfg.Pages[0]
	if pg.Columns != 2 || len(pg.Boxes) != 2 {
		t.Fatalf("columns = %d, boxes = %d", pg.Columns, len(pg.Boxes))
	}
	if got := feeds(t, cfg, 0); got[0] != "https://reddit.example/.rss" {
		t.Errorf("first column feeds = %v", got)
	}
	if !pg.Choices[0].Enabled || pg.Choices[2].Enabled || len(pg.Choices[0].MissingSecrets) != 0 {
		t.Errorf("choices = %+v", pg.Choices)
	}

	// over max_columns: the first two (config order) win
	if err := SetPageColumns(LayoutPath(cfgPath), "Accueil", []string{"Aujourd'hui", "Niches", "Nouvelles"}); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c := cfg.Pages[0].Choices; !c[0].Enabled || !c[1].Enabled || c[2].Enabled {
		t.Errorf("capped choices = %+v", c)
	}

	// a saved choice naming no current column falls back to the config's flags
	if err := SetPageColumns(LayoutPath(cfgPath), "Accueil", []string{"Gone"}); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c := cfg.Pages[0].Choices; c[0].Enabled || !c[1].Enabled || !c[2].Enabled {
		t.Errorf("fallback choices = %+v", c)
	}
}

func TestLoadExampleConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	data, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cfgPath, string(data), 0o644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// the shipped default must work with no secrets.yaml at all
	for _, pg := range cfg.Pages {
		for _, c := range pg.Choices {
			if c.Enabled && len(c.MissingSecrets) > 0 {
				t.Errorf("page %q column %q is on by default but needs %v", pg.Name, c.Name, c.MissingSecrets)
			}
		}
	}
	if pg := cfg.Pages[0]; pg.Columns != 2 || pg.Choices[1].Name != "Nouvelles" || !pg.Choices[1].Enabled {
		t.Errorf("default Accueil = %d columns, choices %+v", pg.Columns, pg.Choices)
	}
}
