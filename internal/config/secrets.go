package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// tokenRe matches ${NAME} and ${secret:NAME} placeholders.
var tokenRe = regexp.MustCompile(`\$\{([A-Za-z0-9_.:-]+)\}`)

// expandTokens substitutes ${VAR} (environment) and ${secret:key} (secrets file)
// placeholders in the raw config bytes before YAML parsing, so a token works
// anywhere — key, scalar value, or list element. Quote the token in the config
// (`"${secret:x}"`); a secret value must not itself contain a double quote.
// Unresolved tokens become empty and are logged.
func expandTokens(data []byte, secrets map[string]string) []byte {
	return tokenRe.ReplaceAllFunc(data, func(m []byte) []byte {
		name := string(tokenRe.FindSubmatch(m)[1])
		if key, ok := strings.CutPrefix(name, "secret:"); ok {
			if v, ok := secrets[key]; ok {
				return []byte(v)
			}
			slog.Warn("config: unresolved secret", "key", key)
			return nil
		}
		if v, ok := os.LookupEnv(name); ok {
			return []byte(v)
		}
		slog.Warn("config: unresolved environment variable", "name", name)
		return nil
	})
}

// loadSecrets reads a flat key: value YAML file. A missing file is fine (returns
// an empty map); a present file must be mode 0600 or stricter.
func loadSecrets(path string) (map[string]string, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf("%s: permissions %o are too open, run: chmod 600 %s", path, perm, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

// SecretsPath is the secrets file that sits beside the config file.
func SecretsPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "secrets.yaml")
}

// ReadSecretKeys reads only the requested keys from the secrets file, omitting
// any that aren't set. Unlike loadSecrets, a missing file or an unreadable one
// just yields an empty result — callers that only want a status check (is
// spotify connected?) shouldn't have to handle "file doesn't exist yet" as an
// error.
func ReadSecretKeys(path string, keys ...string) (map[string]string, error) {
	all, err := loadSecrets(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := all[k]; ok && v != "" {
			out[k] = v
		}
	}
	return out, nil
}

// SetSecrets updates or appends key: value pairs in the secrets file at path,
// preserving existing keys' order, comments, and any keys not mentioned in kv.
// The file is created if missing and is always left at mode 0600 — including
// when it already existed at a looser mode, since os.WriteFile's perm argument
// only takes effect when creating a file.
func SetSecrets(path string, kv map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	var doc yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{
			Kind:    yaml.DocumentNode,
			Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}},
		}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: not a YAML mapping", path)
	}

	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic order for newly-appended keys

	for _, k := range keys {
		if i := findSecretKey(root, k); i >= 0 {
			root.Content[i+1].SetString(kv[k]) // in place: keeps that node's comments
			continue
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: kv[k]})
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func findSecretKey(root *yaml.Node, key string) int {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return i
		}
	}
	return -1
}
