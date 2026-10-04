package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetSecretsAppendsToEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.yaml")

	if err := SetSecrets(path, map[string]string{"spotify_client_id": "abc123"}); err != nil {
		t.Fatal(err)
	}

	got, err := ReadSecretKeys(path, "spotify_client_id")
	if err != nil {
		t.Fatal(err)
	}
	if got["spotify_client_id"] != "abc123" {
		t.Fatalf("got %q, want abc123", got["spotify_client_id"])
	}
}

func TestSetSecretsPreservesCommentsAndOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.yaml")
	writeFile(t, path, `# Your Reddit home feed — reddit.com/prefs/feeds/
reddit_home: "https://www.reddit.com/.rss?feed=XXXXXXXX&user=YOURNAME"
`, 0o600)

	if err := SetSecrets(path, map[string]string{"reddit_home": "https://www.reddit.com/.rss?feed=NEW&user=me"}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "# Your Reddit home feed") {
		t.Errorf("comment was dropped:\n%s", raw)
	}

	all, err := loadSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	if all["reddit_home"] != "https://www.reddit.com/.rss?feed=NEW&user=me" {
		t.Errorf("value not updated: %q", all["reddit_home"])
	}
}

func TestSetSecretsAddsNewKeyAlongsideExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.yaml")
	writeFile(t, path, "reddit_home: https://reddit.example/.rss\n", 0o600)

	if err := SetSecrets(path, map[string]string{"spotify_refresh_token": "rt-1"}); err != nil {
		t.Fatal(err)
	}

	all, err := loadSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	if all["reddit_home"] != "https://reddit.example/.rss" {
		t.Errorf("existing key lost: %v", all)
	}
	if all["spotify_refresh_token"] != "rt-1" {
		t.Errorf("new key not added: %v", all)
	}
}

func TestSetSecretsFixesLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.yaml")
	writeFile(t, path, "reddit_home: https://reddit.example/.rss\n", 0o644)

	if err := SetSecrets(path, map[string]string{"spotify_client_id": "abc"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("mode %o still too open after SetSecrets", perm)
	}
}

func TestReadSecretKeysMissingFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadSecretKeys(filepath.Join(dir, "secrets.yaml"), "spotify_client_id")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty map, got %v", got)
	}
}

func TestReadSecretKeysOmitsUnsetKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.yaml")
	writeFile(t, path, "reddit_home: https://reddit.example/.rss\n", 0o600)

	got, err := ReadSecretKeys(path, "reddit_home", "spotify_client_id")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["spotify_client_id"]; ok {
		t.Errorf("unset key should be omitted, got %v", got)
	}
	if got["reddit_home"] == "" {
		t.Errorf("set key missing: %v", got)
	}
}

func TestListSecretKeysMissingFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	got, err := ListSecretKeys(filepath.Join(dir, "secrets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty list, got %v", got)
	}
}

func TestListSecretKeysSortedAndExcludesEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.yaml")
	writeFile(t, path, "zeta: x\nreddit_home: y\nblank: \"\"\n", 0o600)

	got, err := ListSecretKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"reddit_home", "zeta"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDeleteSecretRemovesOnlyThatKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.yaml")
	writeFile(t, path, "# keep me\nreddit_home: y\nspotify_client_id: z\n", 0o600)

	if err := DeleteSecret(path, "spotify_client_id"); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "# keep me") {
		t.Errorf("unrelated comment was dropped:\n%s", raw)
	}

	all, err := loadSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := all["spotify_client_id"]; ok {
		t.Errorf("key should be gone: %v", all)
	}
	if all["reddit_home"] != "y" {
		t.Errorf("unrelated key should survive: %v", all)
	}
}

func TestDeleteSecretMissingKeyIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.yaml")
	writeFile(t, path, "reddit_home: y\n", 0o600)

	if err := DeleteSecret(path, "does_not_exist"); err != nil {
		t.Fatal(err)
	}
	all, err := loadSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	if all["reddit_home"] != "y" {
		t.Errorf("existing key should be untouched: %v", all)
	}
}

func TestDeleteSecretMissingFileIsNoop(t *testing.T) {
	dir := t.TempDir()
	if err := DeleteSecret(filepath.Join(dir, "secrets.yaml"), "anything"); err != nil {
		t.Fatal(err)
	}
}

func TestExpandTokensRefusesBreakout(t *testing.T) {
	got := expandTokens([]byte(`feeds: [ "${secret:a}", "${secret:b}" ]`), map[string]string{
		"a": "https://ok.example/rss?x=1&y=2",
		"b": "x\" ]\nwidgets: [",
	})
	if want := `feeds: [ "https://ok.example/rss?x=1&y=2", "" ]`; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
