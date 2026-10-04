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
//	pages:
//	  Accueil: true
//	  Media: false
//	columns:
//	  Accueil:
//	    Niches: false
//	    Nouvelles: true
//
// A page or column the file doesn't mention — one added to config.yaml after
// the last save — uses its `enabled:` flag from config.yaml.
type Layout struct {
	Pages   Toggles            `yaml:"pages"`
	Columns map[string]Toggles `yaml:"columns"`
}

// Toggles is an on/off choice per name. Earlier versions of the file wrote a
// list of the names that were on; such a list is Exclusive — names missing
// from it are off — and one naming nothing current is ignored.
type Toggles struct {
	On        map[string]bool
	Exclusive bool
}

func (t *Toggles) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.SequenceNode {
		var names []string
		if err := n.Decode(&names); err != nil {
			return err
		}
		t.On, t.Exclusive = map[string]bool{}, true
		for _, name := range names {
			t.On[name] = true
		}
		return nil
	}
	return n.Decode(&t.On)
}

func (t Toggles) MarshalYAML() (any, error) { return t.On, nil }

// resolve says which of names are on, given each one's config default.
func (t Toggles) resolve(names []string, defaults []bool) []bool {
	if t.Exclusive {
		matched := false
		for _, n := range names {
			matched = matched || t.On[n]
		}
		if !matched {
			return defaults
		}
	}
	out := make([]bool, len(names))
	for i, n := range names {
		v, ok := t.On[n]
		switch {
		case ok:
			out[i] = v
		case t.Exclusive:
			out[i] = false
		default:
			out[i] = defaults[i]
		}
	}
	return out
}

// LayoutPath is the layout file that sits beside the config file.
func LayoutPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "layout.yaml")
}

// loadLayout reads the layout file. A missing file is fine (empty layout).
func loadLayout(path string) (Layout, error) {
	l := Layout{Columns: map[string]Toggles{}}
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
	legacy := false // the first shape: page -> [columns] at top level
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
		l.Columns = map[string]Toggles{}
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
