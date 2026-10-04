package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/maxbasque/quarks/internal/core"
)

type Window struct {
	Theme string `yaml:"theme"`
	// columns / column_weights here apply to a legacy single-page config (a
	// top-level `widgets:` list). With `pages:`, set them per page.
	Columns       int       `yaml:"columns"`
	ColumnWeights []float64 `yaml:"column_weights"`
}

// Page is one top-level tab of the app: its own column layout and cards.
type Page struct {
	Name string
	// Enabled is false for a page switched off (`enabled: false`, or from
	// Settings). It's still fully parsed so Settings can list it and its
	// columns, but the app neither shows nor fetches it.
	Enabled       bool
	Columns       int
	ColumnWeights []float64
	Boxes         []Box

	// Choices is set when the page declares named columns: every column the
	// config offers, shown or not, in config order. Settings toggles them.
	// Boxes / Columns / ColumnWeights above hold only the enabled ones.
	Choices    []ColumnChoice
	MaxColumns int
}

// ColumnChoice is one named, toggleable column of a page.
type ColumnChoice struct {
	Name    string
	Enabled bool
	// MissingSecrets lists ${secret:key} references in this column's widgets
	// that secrets.yaml doesn't define — Settings flags them.
	MissingSecrets []string
}

// Box is one card. It holds one widget, or several shown as tabs (`type: group`).
type Box struct {
	Column  int
	Title   string
	Widgets []core.WidgetConfig
}

type Config struct {
	Window Window
	Pages  []Page
}

type fileShape struct {
	Window  Window      `yaml:"window"`
	Widgets []yaml.Node `yaml:"widgets"` // legacy: a single implicit page
	Pages   []pageShape `yaml:"pages"`
}

type pageShape struct {
	Name    string `yaml:"name"`
	Enabled *bool  `yaml:"enabled"` // default true
	// Columns is either a count (cards place themselves with `column: N`) or
	// a list of named columns, each holding its own widgets (columnShape).
	Columns       yaml.Node   `yaml:"columns"`
	MaxColumns    int         `yaml:"max_columns"`
	ColumnWeights []float64   `yaml:"column_weights"`
	Widgets       []yaml.Node `yaml:"widgets"`
}

type columnShape struct {
	Name    string      `yaml:"name"`
	Enabled *bool       `yaml:"enabled"` // default true
	Weight  float64     `yaml:"weight"`
	Widgets []yaml.Node `yaml:"widgets"`
}

type groupShape struct {
	Type   string      `yaml:"type"`
	Column int         `yaml:"column"`
	Title  string      `yaml:"title"`
	Tabs   []yaml.Node `yaml:"tabs"`
}

// Load reads and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	secrets, err := loadSecrets(SecretsPath(path))
	if err != nil {
		return nil, err
	}
	layout, err := loadLayout(LayoutPath(path))
	if err != nil {
		return nil, err
	}

	// the raw (unexpanded) tree is only for spotting unresolved secrets per
	// named column; everything else reads the expanded one
	var raw fileShape
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	data = expandTokens(data, secrets)

	var fs fileShape
	if err := yaml.Unmarshal(data, &fs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	cfg := &Config{Window: fs.Window}
	if cfg.Window.Theme == "" {
		cfg.Window.Theme = "dark"
	}

	shapes := fs.Pages
	if len(shapes) == 0 {
		// legacy: top-level widgets → one unnamed page
		shapes = []pageShape{{
			ColumnWeights: fs.Window.ColumnWeights,
			Widgets:       fs.Widgets,
		}}
		if fs.Window.Columns != 0 {
			shapes[0].Columns.Encode(fs.Window.Columns)
		}
	}

	names := map[string]bool{}
	for i, ps := range shapes {
		if ps.Name != "" && names[ps.Name] {
			return nil, fmt.Errorf("%s: page %q listed twice", path, ps.Name)
		}
		names[ps.Name] = true
		page := Page{Name: ps.Name, Enabled: ps.Enabled == nil || *ps.Enabled, ColumnWeights: ps.ColumnWeights}

		switch ps.Columns.Kind {
		case yaml.SequenceNode:
			var rawCols []columnShape
			if i < len(raw.Pages) {
				_ = raw.Pages[i].Columns.Decode(&rawCols)
			}
			if err := loadNamedColumns(&page, ps, rawCols, layout.Columns[ps.Name], secrets); err != nil {
				return nil, fmt.Errorf("page %q: %w", ps.Name, err)
			}
		case 0:
		default:
			if err := ps.Columns.Decode(&page.Columns); err != nil {
				return nil, fmt.Errorf("page %q: columns: %w", ps.Name, err)
			}
		}
		if page.Columns == 0 {
			page.Columns = 3
		}
		for j, node := range ps.Widgets {
			box, err := parseBox(node)
			if err != nil {
				return nil, fmt.Errorf("page %d, widget %d: %w", i, j, err)
			}
			page.Boxes = append(page.Boxes, box)
		}
		if len(page.Boxes) == 0 {
			return nil, fmt.Errorf("%s: page %q has no widgets", path, page.Name)
		}
		cfg.Pages = append(cfg.Pages, page)
	}
	applyPageChoice(cfg.Pages, layout.Pages)

	return cfg, nil
}

// applyPageChoice overrides the pages' `enabled:` flags with the Settings
// page's saved choice, when there is one naming at least one current page,
// and makes sure at least one page stays shown.
func applyPageChoice(pages []Page, shown []string) {
	if shown != nil {
		want := map[string]bool{}
		for _, n := range shown {
			want[n] = true
		}
		matched := false
		for _, pg := range pages {
			matched = matched || want[pg.Name]
		}
		if matched {
			for i := range pages {
				pages[i].Enabled = want[pages[i].Name]
			}
		}
	}
	for _, pg := range pages {
		if pg.Enabled {
			return
		}
	}
	pages[0].Enabled = true
}

// loadNamedColumns fills page from a `columns:` list. enabled, when non-nil,
// is the Settings page's saved choice for this page and overrides each
// column's `enabled:` flag. Only enabled columns become boxes, so a hidden
// column's widgets are never fetched.
func loadNamedColumns(page *Page, ps pageShape, rawCols []columnShape, enabled []string, secrets map[string]string) error {
	var cols []columnShape
	if err := ps.Columns.Decode(&cols); err != nil {
		return fmt.Errorf("columns: %w", err)
	}
	page.MaxColumns = ps.MaxColumns
	if page.MaxColumns <= 0 {
		page.MaxColumns = 3
	}

	on := make([]bool, len(cols))
	seen := map[string]bool{}
	for k, c := range cols {
		if c.Name == "" {
			return fmt.Errorf("column %d: missing name", k)
		}
		if seen[c.Name] {
			return fmt.Errorf("column %q listed twice", c.Name)
		}
		seen[c.Name] = true
		on[k] = c.Enabled == nil || *c.Enabled
	}
	if enabled != nil {
		want := map[string]bool{}
		for _, n := range enabled {
			want[n] = true
		}
		override := make([]bool, len(cols))
		matched := false
		for k, c := range cols {
			override[k] = want[c.Name]
			matched = matched || override[k]
		}
		if matched { // a saved choice naming no current column falls back to the config's
			on = override
		}
	}

	shown := 0
	for k, c := range cols {
		if on[k] && shown >= page.MaxColumns {
			on[k] = false
		}
		choice := ColumnChoice{Name: c.Name, Enabled: on[k]}
		if k < len(rawCols) {
			choice.MissingSecrets = missingSecrets(rawCols[k].Widgets, secrets)
		}
		page.Choices = append(page.Choices, choice)
		if !on[k] {
			continue
		}
		shown++
		page.ColumnWeights = append(page.ColumnWeights, c.Weight)
		for j, node := range c.Widgets {
			box, err := parseBox(node)
			if err != nil {
				return fmt.Errorf("column %q, widget %d: %w", c.Name, j, err)
			}
			box.Column = shown
			for w := range box.Widgets {
				box.Widgets[w].Column = shown
			}
			page.Boxes = append(page.Boxes, box)
		}
	}
	page.Columns = shown

	// weights are optional; any unset one drops them all back to equal widths
	for _, w := range page.ColumnWeights {
		if w <= 0 {
			page.ColumnWeights = nil
			break
		}
	}
	return nil
}

func parseBox(node yaml.Node) (Box, error) {
	var probe struct {
		Type string `yaml:"type"`
	}
	if err := node.Decode(&probe); err != nil {
		return Box{}, err
	}

	if probe.Type == "group" || probe.Type == "tabs" {
		var g groupShape
		if err := node.Decode(&g); err != nil {
			return Box{}, err
		}
		if len(g.Tabs) == 0 {
			return Box{}, fmt.Errorf("group %q: no tabs", g.Title)
		}
		column := g.Column
		if column == 0 {
			column = 1
		}
		box := Box{Column: column, Title: g.Title}
		for j, tab := range g.Tabs {
			wc, err := core.ParseWidget(tab)
			if err != nil {
				return Box{}, fmt.Errorf("tab %d: %w", j, err)
			}
			if wc.Type == "" {
				return Box{}, fmt.Errorf("tab %d: missing type", j)
			}
			wc.Column = column
			box.Widgets = append(box.Widgets, wc)
		}
		return box, nil
	}

	wc, err := core.ParseWidget(node)
	if err != nil {
		return Box{}, err
	}
	if wc.Type == "" {
		return Box{}, fmt.Errorf("missing type")
	}
	return Box{Column: wc.Column, Widgets: []core.WidgetConfig{wc}}, nil
}
