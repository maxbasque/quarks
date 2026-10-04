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

// The layout file holds choices made from the Settings page — which pages
// and which named columns are shown — so the app never has to rewrite the
// hand-edited config.yaml:
//
//	pages: [Accueil, Sports]
//	columns:
//	  Accueil: [Nouvelles, Aujourd'hui]
//
// Anything the file doesn't mention uses the `enabled:` flags in config.yaml.
// An older form holding only the columns map at top level is still read.
type Layout struct {
	Pages   []string            `yaml:"pages"` // nil: not chosen yet
	Columns map[string][]string `yaml:"columns"`
}

// LayoutPath is the layout file that sits beside the config file.
func LayoutPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "layout.yaml")
}

// loadLayout reads the layout file. A missing file is fine (empty layout).
func loadLayout(path string) (Layout, error) {
	l := Layout{Columns: map[string][]string{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, err
	}

	var top map[string]yaml.Node
	if err := yaml.Unmarshal(data, &top); err != nil {
		return l, fmt.Errorf("parse %s: %w", path, err)
	}
	legacy := false
	for k := range top {
		legacy = legacy || (k != "pages" && k != "columns")
	}
	if legacy {
		err = yaml.Unmarshal(data, &l.Columns)
	} else {
		err = yaml.Unmarshal(data, &l)
	}
	if err != nil {
		return l, fmt.Errorf("parse %s: %w", path, err)
	}
	if l.Columns == nil {
		l.Columns = map[string][]string{}
	}
	return l, nil
}

// SaveLayout writes the layout file whole.
func SaveLayout(path string, l Layout) error {
	out, err := yaml.Marshal(l)
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
