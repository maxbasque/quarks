package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
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

// secretsPath is the secrets file that sits beside the config file.
func secretsPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "secrets.yaml")
}
