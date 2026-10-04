package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maxbasque/quarks/internal/config"
	"github.com/maxbasque/quarks/internal/core"
	"github.com/maxbasque/quarks/internal/spotifyapi"
)

func testServer(t *testing.T, spotifyFixture *httptest.Server) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := core.NewStore(filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	sp := spotifyapi.NewClient()
	if spotifyFixture != nil {
		sp.AuthBase = spotifyFixture.URL
		sp.APIBase = spotifyFixture.URL
	}
	s, err := NewServer(store, func(string) bool { return true }, filepath.Join(dir, "secrets.yaml"), filepath.Join(dir, "layout.yaml"), func() error { return nil }, sp)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7373"+path, nil)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func postForm(t *testing.T, s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7373"+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	return w
}

func TestSettingsPageNotConfigured(t *testing.T) {
	s := testServer(t, nil)
	w := get(t, s, "/settings")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Not configured") {
		t.Errorf("expected \"Not configured\", got:\n%s", w.Body.String())
	}
}

func TestSpotifyAuthorizeRequiresCredentials(t *testing.T) {
	s := testServer(t, nil)
	w := get(t, s, "/settings/spotify/authorize")
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/settings?err=no_credentials" {
		t.Errorf("Location = %q", loc)
	}
}

func TestSpotifyAuthorizeRedirectsWithState(t *testing.T) {
	s := testServer(t, nil)
	if err := config.SetSecrets(s.secretsPath, map[string]string{
		"spotify_client_id": "cid", "spotify_client_secret": "csecret",
	}); err != nil {
		t.Fatal(err)
	}

	w := get(t, s, "/settings/spotify/authorize")
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d", w.Code)
	}
	loc := w.Header().Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("no state in authorize redirect")
	}
	pending := s.spotifyPendingState.Load()
	if pending == nil || pending.state != state {
		t.Errorf("pending state not stored: %+v", pending)
	}
	if got := u.Query().Get("redirect_uri"); got != "http://127.0.0.1:7373/settings/spotify/callback" {
		t.Errorf("redirect_uri = %q", got)
	}
}

func TestSpotifyCallbackStateMismatch(t *testing.T) {
	s := testServer(t, nil)
	s.spotifyPendingState.Store(&spotifyPending{state: "expected", expires: time.Now().Add(time.Minute)})

	w := get(t, s, "/settings/spotify/callback?code=abc&state=wrong")
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/settings?err=state_mismatch" {
		t.Errorf("Location = %q", loc)
	}
}

func TestSpotifyCallbackExpiredState(t *testing.T) {
	s := testServer(t, nil)
	s.spotifyPendingState.Store(&spotifyPending{state: "s1", expires: time.Now().Add(-time.Minute)})

	w := get(t, s, "/settings/spotify/callback?code=abc&state=s1")
	if loc := w.Header().Get("Location"); loc != "/settings?err=state_mismatch" {
		t.Errorf("Location = %q", loc)
	}
}

func TestSpotifyCallbackDenied(t *testing.T) {
	s := testServer(t, nil)
	w := get(t, s, "/settings/spotify/callback?error=access_denied")
	if loc := w.Header().Get("Location"); loc != "/settings?err=denied" {
		t.Errorf("Location = %q", loc)
	}
}

func fakeSpotifyFixture(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at-1", "refresh_token": "rt-1", "expires_in": 3600,
			})
		case r.URL.Path == "/me":
			_ = json.NewEncoder(w).Encode(map[string]any{"display_name": "Max"})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestSpotifyCallbackSuccessWritesSecretAndReloads(t *testing.T) {
	fixture := fakeSpotifyFixture(t)
	defer fixture.Close()
	s := testServer(t, fixture)

	if err := config.SetSecrets(s.secretsPath, map[string]string{
		"spotify_client_id": "cid", "spotify_client_secret": "csecret",
	}); err != nil {
		t.Fatal(err)
	}
	s.spotifyPendingState.Store(&spotifyPending{state: "s1", expires: time.Now().Add(time.Minute)})

	reloaded := false
	s.reloadNow = func() error { reloaded = true; return nil }

	w := get(t, s, "/settings/spotify/callback?code=abc&state=s1")
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/settings" {
		t.Fatalf("status=%d location=%q", w.Code, w.Header().Get("Location"))
	}
	if !reloaded {
		t.Error("reloadNow was not called")
	}
	if s.spotifyPendingState.Load() != nil {
		t.Error("pending state should be cleared after use (one-shot)")
	}

	secrets, err := config.ReadSecretKeys(s.secretsPath, "spotify_refresh_token")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["spotify_refresh_token"] != "rt-1" {
		t.Errorf("refresh token not persisted: %v", secrets)
	}

	page := get(t, s, "/settings")
	if !strings.Contains(page.Body.String(), "Connected as Max") {
		t.Errorf("expected \"Connected as Max\":\n%s", page.Body.String())
	}
}

func TestSpotifyDisconnectClearsToken(t *testing.T) {
	s := testServer(t, nil)
	if err := config.SetSecrets(s.secretsPath, map[string]string{
		"spotify_client_id": "cid", "spotify_client_secret": "csecret",
		"spotify_refresh_token": "rt-1",
	}); err != nil {
		t.Fatal(err)
	}
	s.spotifyStatus.Store(&spotifyStatus{displayName: "Max"})

	w := postForm(t, s, "/settings/spotify/disconnect", url.Values{})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}

	secrets, err := config.ReadSecretKeys(s.secretsPath, "spotify_refresh_token", "spotify_client_id")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["spotify_refresh_token"] != "" {
		t.Errorf("refresh token not cleared: %v", secrets)
	}
	if secrets["spotify_client_id"] == "" {
		t.Error("client_id should survive a disconnect")
	}
	if s.spotifyStatus.Load() != nil {
		t.Error("cached status should be cleared on disconnect")
	}
}

func TestSpotifyCredentialsFormOnlyOverwritesProvidedFields(t *testing.T) {
	s := testServer(t, nil)
	if err := config.SetSecrets(s.secretsPath, map[string]string{"spotify_client_id": "existing-id"}); err != nil {
		t.Fatal(err)
	}

	w := postForm(t, s, "/settings/spotify/credentials", url.Values{"client_secret": {"new-secret"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}

	secrets, err := config.ReadSecretKeys(s.secretsPath, "spotify_client_id", "spotify_client_secret")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["spotify_client_id"] != "existing-id" {
		t.Errorf("client_id should be untouched: %v", secrets)
	}
	if secrets["spotify_client_secret"] != "new-secret" {
		t.Errorf("client_secret not saved: %v", secrets)
	}
}

func TestSecretsSetAndListAndDelete(t *testing.T) {
	s := testServer(t, nil)

	w := postForm(t, s, "/settings/secrets/set", url.Values{"key": {"reddit_home"}, "value": {"https://reddit.example/.rss"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings#secrets" {
		t.Fatalf("status=%d location=%q", w.Code, w.Header().Get("Location"))
	}

	page := get(t, s, "/settings")
	if !strings.Contains(page.Body.String(), "reddit_home") {
		t.Errorf("expected reddit_home to be listed:\n%s", page.Body.String())
	}
	if strings.Contains(page.Body.String(), "reddit.example") {
		t.Error("secret value should never be echoed back into the page")
	}

	w = postForm(t, s, "/settings/secrets/delete", url.Values{"key": {"reddit_home"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	secrets, err := config.ReadSecretKeys(s.secretsPath, "reddit_home")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["reddit_home"] != "" {
		t.Errorf("key should be deleted: %v", secrets)
	}
}

func TestSecretsSetRejectsBadKey(t *testing.T) {
	s := testServer(t, nil)
	w := postForm(t, s, "/settings/secrets/set", url.Values{"key": {"Not A Valid Key!"}, "value": {"x"}})
	if loc := w.Header().Get("Location"); loc != "/settings?serr=bad_key#secrets" {
		t.Errorf("Location = %q", loc)
	}
}

func TestSecretsSetRejectsReservedKey(t *testing.T) {
	s := testServer(t, nil)
	w := postForm(t, s, "/settings/secrets/set", url.Values{"key": {"spotify_client_id"}, "value": {"sneaky"}})
	if loc := w.Header().Get("Location"); loc != "/settings?serr=reserved_key#secrets" {
		t.Errorf("Location = %q", loc)
	}
	secrets, err := config.ReadSecretKeys(s.secretsPath, "spotify_client_id")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["spotify_client_id"] != "" {
		t.Error("the generic secrets form should not be able to write a reserved key")
	}
}

func TestSecretsSetRejectsEmptyValue(t *testing.T) {
	s := testServer(t, nil)
	w := postForm(t, s, "/settings/secrets/set", url.Values{"key": {"reddit_home"}, "value": {""}})
	if loc := w.Header().Get("Location"); loc != "/settings?serr=empty_value#secrets" {
		t.Errorf("Location = %q", loc)
	}
}

func TestSecretsDeleteOfReservedKeyIsNoop(t *testing.T) {
	s := testServer(t, nil)
	if err := config.SetSecrets(s.secretsPath, map[string]string{"spotify_client_id": "keep-me"}); err != nil {
		t.Fatal(err)
	}
	postForm(t, s, "/settings/secrets/delete", url.Values{"key": {"spotify_client_id"}})
	secrets, err := config.ReadSecretKeys(s.secretsPath, "spotify_client_id")
	if err != nil {
		t.Fatal(err)
	}
	if secrets["spotify_client_id"] != "keep-me" {
		t.Error("the generic secrets delete should not remove a reserved key")
	}
}

func layoutServer(t *testing.T) *Server {
	t.Helper()
	s := testServer(t, nil)
	s.Publish(Meta{Layout: []LayoutPage{
		{Name: "Accueil", Enabled: true, MaxColumns: 2, Columns: []config.ColumnChoice{
			{Name: "Niches", MissingSecrets: []string{"reddit_home"}},
			{Name: "Nouvelles", Enabled: true},
			{Name: "Aujourd'hui", Enabled: true},
		}},
		{Name: "Sports", MaxColumns: 3, Columns: []config.ColumnChoice{
			{Name: "Canadiens", Enabled: true},
			{Name: "Classements", Enabled: true},
		}},
		{Name: "Media", Enabled: true},
	}})
	return s
}

func TestSettingsLayoutSection(t *testing.T) {
	s := layoutServer(t)
	body := get(t, s, "/settings").Body.String()
	for _, want := range []string{
		`id="layout"`, `name="page" value="Media"`, `name="cols.Accueil" value="Niches"`,
		`name="cols.Sports" value="Canadiens"`, `<code>reddit_home</code>`, `data-max="2"`, "2 of 3 pages shown",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page missing %q", want)
		}
	}
}

func TestLayoutSet(t *testing.T) {
	s := layoutServer(t)

	// form order doesn't matter; the saved order is the config's
	w := postForm(t, s, "/settings/layout", url.Values{
		"page":         {"Sports", "Accueil"},
		"cols.Accueil": {"Aujourd'hui", "Niches"},
		"cols.Sports":  {"Classements"},
		"cols.Bogus":   {"x"},
	})
	if loc := w.Header().Get("Location"); w.Code != http.StatusSeeOther || loc != "/settings#layout" {
		t.Fatalf("status = %d, Location = %q", w.Code, loc)
	}
	data, err := os.ReadFile(s.layoutPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "pages:\n    - Accueil\n    - Sports\ncolumns:\n    Accueil:\n        - Niches\n        - Aujourd'hui\n    Sports:\n        - Classements\n"
	if got := string(data); got != want {
		t.Errorf("layout.yaml =\n%s\nwant\n%s", got, want)
	}

	ok := url.Values{"cols.Accueil": {"Nouvelles"}, "cols.Sports": {"Canadiens"}}
	with := func(extra url.Values) url.Values {
		v := url.Values{}
		for k, vs := range ok {
			v[k] = vs
		}
		for k, vs := range extra {
			v[k] = vs
		}
		return v
	}
	for _, tc := range []struct {
		form url.Values
		code string
	}{
		{with(url.Values{"page": {"Gone"}}), "no_pages"},
		{with(url.Values{"page": {"Media"}, "cols.Sports": nil}), "no_columns"},
		{with(url.Values{"page": {"Media"}, "cols.Accueil": {"Niches", "Nouvelles", "Aujourd'hui"}}), "too_many"},
	} {
		w := postForm(t, s, "/settings/layout", tc.form)
		if loc := w.Header().Get("Location"); loc != "/settings?lerr="+tc.code+"#layout" {
			t.Errorf("%v: Location = %q", tc.form, loc)
		}
	}
}

func TestRejectsCrossSitePost(t *testing.T) {
	s := testServer(t, nil)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7373/settings/secrets/set",
		strings.NewReader(url.Values{"key": {"reddit_home"}, "value": {"https://evil.example/rss"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
	if keys, _ := config.ListSecretKeys(s.secretsPath); len(keys) != 0 {
		t.Errorf("secret written anyway: %v", keys)
	}
}

func TestRejectsUnknownHost(t *testing.T) {
	s := testServer(t, nil)
	for host, want := range map[string]int{
		"evil.example:7373": http.StatusForbidden, // DNS rebinding
		"localhost:7373":    http.StatusOK,
		"127.0.0.1:7373":    http.StatusOK,
		"[::1]:7373":        http.StatusOK,
		"192.168.1.20:7373": http.StatusOK, // --addr on the LAN, reached by IP
	} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/settings", nil)
		w := httptest.NewRecorder()
		s.Routes().ServeHTTP(w, req)
		if w.Code != want {
			t.Errorf("Host %s: status = %d, want %d", host, w.Code, want)
		}
	}
}

func TestSecretsSetRejectsYAMLBreakout(t *testing.T) {
	s := testServer(t, nil)
	w := postForm(t, s, "/settings/secrets/set", url.Values{"key": {"reddit_home"}, "value": {"x\"]\n  - type: rss"}})
	if loc := w.Header().Get("Location"); loc != "/settings?serr=bad_value#secrets" {
		t.Errorf("Location = %q", loc)
	}
	if keys, _ := config.ListSecretKeys(s.secretsPath); len(keys) != 0 {
		t.Errorf("secret written anyway: %v", keys)
	}
}
