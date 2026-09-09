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
	Name          string
	Columns       int
	ColumnWeights []float64
	Boxes         []Box
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
	Name          string      `yaml:"name"`
	Columns       int         `yaml:"columns"`
	ColumnWeights []float64   `yaml:"column_weights"`
	Widgets       []yaml.Node `yaml:"widgets"`
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

	secrets, err := loadSecrets(secretsPath(path))
	if err != nil {
		return nil, err
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
			Columns:       fs.Window.Columns,
			ColumnWeights: fs.Window.ColumnWeights,
			Widgets:       fs.Widgets,
		}}
	}

	for i, ps := range shapes {
		page := Page{
			Name:          ps.Name,
			Columns:       ps.Columns,
			ColumnWeights: ps.ColumnWeights,
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

	return cfg, nil
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
