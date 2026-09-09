package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/maxbasque/quarks/internal/core"
)

type Window struct {
	Columns int    `yaml:"columns"`
	Theme   string `yaml:"theme"`
}

// Box is one card on the dashboard. It holds one widget, or several shown as
// tabs (config `type: group`).
type Box struct {
	Column  int
	Title   string // optional label; for a tab group
	Widgets []core.WidgetConfig
}

type Config struct {
	Window Window
	Boxes  []Box
}

type fileShape struct {
	Window  Window      `yaml:"window"`
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
	if cfg.Window.Columns == 0 {
		cfg.Window.Columns = 3
	}
	if cfg.Window.Theme == "" {
		cfg.Window.Theme = "dark"
	}

	for i, node := range fs.Widgets {
		box, err := parseBox(node)
		if err != nil {
			return nil, fmt.Errorf("widget %d: %w", i, err)
		}
		cfg.Boxes = append(cfg.Boxes, box)
	}

	if len(cfg.Boxes) == 0 {
		return nil, fmt.Errorf("%s: no widgets configured", path)
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
			wc.Column = column // tabs share the group's column
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
