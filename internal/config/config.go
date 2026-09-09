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

type Config struct {
	Window  Window
	Widgets []core.WidgetConfig
}

type fileShape struct {
	Window  Window      `yaml:"window"`
	Widgets []yaml.Node `yaml:"widgets"`
}

// Load reads and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

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
		wc, err := core.ParseWidget(node)
		if err != nil {
			return nil, fmt.Errorf("widget %d: %w", i, err)
		}
		if wc.Type == "" {
			return nil, fmt.Errorf("widget %d: missing type", i)
		}
		cfg.Widgets = append(cfg.Widgets, wc)
	}

	if len(cfg.Widgets) == 0 {
		return nil, fmt.Errorf("%s: no widgets configured", path)
	}
	return cfg, nil
}
