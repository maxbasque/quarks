package core

import "fmt"

// Registry maps a widget type name ("rss", "reddit", ...) to its Factory.
// Adding a feed type = one file + one registry line.
type Registry struct {
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

func (r *Registry) Register(name string, f Factory) {
	r.factories[name] = f
}

// Build resolves a widget config to a live Provider.
func (r *Registry) Build(cfg WidgetConfig) (Provider, error) {
	f, ok := r.factories[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown widget type %q", cfg.Type)
	}
	return f(cfg)
}
