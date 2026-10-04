package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// The layout file holds choices made from the Settings page — which named
// columns each page shows — so the app never has to rewrite the hand-edited
// config.yaml. It maps a page name to the names of its enabled columns:
//
//	Accueil: [Nouvelles, Aujourd'hui]
//
// A page missing from the file uses the `enabled:` flags in config.yaml.

// LayoutPath is the layout file that sits beside the config file.
func LayoutPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "layout.yaml")
}

// loadLayout reads the layout file. A missing file is fine (empty map).
func loadLayout(path string) (map[string][]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string][]string
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m == nil {
		m = map[string][]string{}
	}
	return m, nil
}

// SetPageColumns records which named columns page shows, keeping every other
// page's entry in the layout file as it was.
func SetPageColumns(path, page string, columns []string) error {
	m, err := loadLayout(path)
	if err != nil {
		return err
	}
	m[page] = columns
	out, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// missingSecrets lists, in first-seen order, the ${secret:key} references in
// nodes (unexpanded config) that secrets doesn't define.
func missingSecrets(nodes []yaml.Node, secrets map[string]string) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode {
			for _, m := range tokenRe.FindAllStringSubmatch(n.Value, -1) {
				key, ok := strings.CutPrefix(m[1], "secret:")
				if ok && secrets[key] == "" && !seen[key] {
					seen[key] = true
					out = append(out, key)
				}
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	for i := range nodes {
		walk(&nodes[i])
	}
	return out
}
