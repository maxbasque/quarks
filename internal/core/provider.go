package core

import (
	"context"
	"time"

	"gopkg.in/yaml.v3"
)

// Provider is one widget instance's data source. That is the whole contract.
// Credentials never appear here: a secret URL and an OAuth token are both just
// "something this provider was configured with".
type Provider interface {
	Fetch(ctx context.Context) (Payload, error)
}

// Payload is what one fetch produces. Feed widgets fill Items; the handful of
// widgets that don't fit the Item shape fill their own field. Exactly one field
// is populated.
type Payload struct {
	Items     []Item
	Weather   *Weather
	Standings *Standings
}

// Feed is a convenience for the common case.
func Feed(items []Item) Payload { return Payload{Items: items} }

// Factory builds a Provider from its widget config.
type Factory func(cfg WidgetConfig) (Provider, error)

// WidgetConfig is the parsed common shape of a widget entry in the YAML. Provider
// -specific keys (feeds, subreddits, channels, ...) stay in the raw node and are
// pulled out by the factory via Decode, so core knows nothing about them.
type WidgetConfig struct {
	Type   string
	Column int
	Title  string
	TTL    time.Duration
	Limit  int

	raw yaml.Node
}

// Decode unmarshals the full raw widget node into v, letting a provider read its
// own keys into its own struct.
func (w WidgetConfig) Decode(v any) error {
	return w.raw.Decode(v)
}

// widgetYAML is the on-disk shape. TTL is a string here ("15m") because yaml.v3
// will not parse a duration on its own.
type widgetYAML struct {
	Type   string    `yaml:"type"`
	Column int       `yaml:"column"`
	Title  string    `yaml:"title"`
	TTL    string    `yaml:"ttl"`
	Limit  int       `yaml:"limit"`
	Raw    yaml.Node `yaml:"-"`
}

// ParseWidget turns a raw YAML node into a WidgetConfig, applying defaults.
func ParseWidget(node yaml.Node) (WidgetConfig, error) {
	var wy widgetYAML
	if err := node.Decode(&wy); err != nil {
		return WidgetConfig{}, err
	}

	ttl := 15 * time.Minute
	if wy.TTL != "" {
		d, err := time.ParseDuration(wy.TTL)
		if err != nil {
			return WidgetConfig{}, err
		}
		ttl = d
	}

	limit := wy.Limit
	if limit == 0 {
		limit = 15
	}
	column := wy.Column
	if column == 0 {
		column = 1
	}

	return WidgetConfig{
		Type:   wy.Type,
		Column: column,
		Title:  wy.Title,
		TTL:    ttl,
		Limit:  limit,
		raw:    node,
	}, nil
}
