package web

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/maxbasque/quarks/internal/config"
	"github.com/maxbasque/quarks/internal/spotifyapi"
)

const spotifyScope = "user-follow-read"

// mediaSnippet is the config.yaml block a connected user pastes in to get the
// Media tab — connecting only ever writes secrets.yaml (like every other
// integration in this app), it never edits config.yaml on the user's behalf.
// The three client_id/client_secret/refresh_token fields use the same
// ${secret:...} substitution every other credential-backed widget already
// uses (see reddit_home in config.example.yaml) — core.Provider only ever
// sees a WidgetConfig, so this is how it reaches the credentials at all.
const mediaSnippet = `- name: Media
  columns: 1
  widgets:
    - type: spotify
      title: Upcoming releases
      column: 1
      ttl: 15m
      client_id: "${secret:spotify_client_id}"
      client_secret: "${secret:spotify_client_secret}"
      refresh_token: "${secret:spotify_refresh_token}"`

// spotifyPending is an in-flight authorize->callback round trip. Single-user,
// one flow at a time, so a single slot on Server is enough — no session store.
type spotifyPending struct {
	state   string
	expires time.Time
}

// spotifyStatus is the last-known connected identity (or error), cached in
// memory so GET /settings doesn't have to hit Spotify on every view once it's
// been checked once this process.
type spotifyStatus struct {
	displayName string
	err         string
}

var settingsErrors = map[string]string{
	"no_credentials": "Save a Client ID and Client Secret first.",
	"denied":         "Spotify authorization was cancelled or denied.",
	"state_mismatch": "That authorization link expired or was already used — try connecting again.",
	"exchange_failed": "Spotify rejected the authorization code — double-check the Client ID/Secret " +
		"and that the redirect URI below is registered exactly in your Spotify app settings.",
}

// reservedSecretKeys are managed by their own dedicated section (Spotify's
// connect flow) rather than the generic Secrets editor, so the same key isn't
// editable two different ways at once.
var reservedSecretKeys = map[string]bool{
	"spotify_client_id":     true,
	"spotify_client_secret": true,
	"spotify_refresh_token": true,
}

// secretKeyRe restricts hand-entered secret key names to the same shape every
// existing key in secrets.example.yaml already follows — lowercase,
// digits, underscores. Keeps them valid as-is inside a ${secret:key} token.
var secretKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var secretsErrors = map[string]string{
	"bad_key":      "Key names must be lowercase letters, numbers, and underscores, starting with a letter.",
	"reserved_key": "That key is managed by the Spotify section above.",
	"empty_value":  "Enter a value to save.",
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	sp := s.spotifySettingsVM(r)
	if code := r.URL.Query().Get("err"); code != "" {
		if msg, ok := settingsErrors[code]; ok {
			sp.Error = msg
		}
	}

	secrets := s.otherSecretsVM()
	if code := r.URL.Query().Get("serr"); code != "" {
		secrets.Error = secretsErrors[code]
	}

	vm := settingsVM{
		Sections: []sectionVM{
			{ID: "spotify", Label: "Spotify", Status: sp.StatusLine()},
			{ID: "secrets", Label: "Secrets", Status: secrets.StatusLine()},
		},
		Spotify: sp,
		Secrets: secrets,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "settings.html", vm); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// otherSecretsVM lists every secrets.yaml key not already owned by a
// dedicated section (Spotify's) — the generic editor for anything else a
// widget config references via ${secret:key}.
func (s *Server) otherSecretsVM() secretsSettingsVM {
	keys, _ := config.ListSecretKeys(s.secretsPath)
	var vm secretsSettingsVM
	for _, k := range keys {
		if !reservedSecretKeys[k] {
			vm.Keys = append(vm.Keys, k)
		}
	}
	return vm
}

func (s *Server) spotifySettingsVM(r *http.Request) spotifySettingsVM {
	secrets, _ := config.ReadSecretKeys(s.secretsPath, "spotify_client_id", "spotify_client_secret", "spotify_refresh_token")
	sp := spotifySettingsVM{
		ClientIDSet:     secrets["spotify_client_id"] != "",
		ClientSecretSet: secrets["spotify_client_secret"] != "",
		RedirectURI:     spotifyRedirectURI(r),
	}

	refreshToken := secrets["spotify_refresh_token"]
	if refreshToken == "" {
		return sp
	}

	status := s.spotifyStatus.Load()
	if status == nil {
		status = s.checkSpotifyStatus(r, secrets)
	}
	if status.displayName != "" {
		sp.Connected = true
		sp.DisplayName = status.displayName
		sp.MediaSnippet = mediaSnippet
	} else if status.err != "" {
		// A refresh token is on file but we couldn't verify it (network hiccup,
		// or it was revoked on Spotify's side) — still "connected" as far as
		// config goes, just flag it rather than silently claiming success.
		sp.Connected = true
		sp.Error = "Connected, but the last check failed: " + status.err
		sp.MediaSnippet = mediaSnippet
	}
	return sp
}

// checkSpotifyStatus does a live token refresh + WhoAmI to populate the
// display name shown on the settings page, and caches the result. Only
// happens once per process per view-when-uncached — not on every request.
func (s *Server) checkSpotifyStatus(r *http.Request, secrets map[string]string) *spotifyStatus {
	creds := spotifyapi.Credentials{
		ClientID:     secrets["spotify_client_id"],
		ClientSecret: secrets["spotify_client_secret"],
		RefreshToken: secrets["spotify_refresh_token"],
	}
	status := &spotifyStatus{}
	accessToken, _, err := s.spotify.RefreshAccessToken(r.Context(), creds)
	if err != nil {
		status.err = err.Error()
	} else if name, err := s.spotify.WhoAmI(r.Context(), accessToken); err != nil {
		status.err = err.Error()
	} else {
		status.displayName = name
	}
	s.spotifyStatus.Store(status)
	return status
}

func (s *Server) handleSpotifyCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	kv := map[string]string{}
	if v := strings.TrimSpace(r.FormValue("client_id")); v != "" {
		kv["spotify_client_id"] = v
	}
	if v := strings.TrimSpace(r.FormValue("client_secret")); v != "" {
		kv["spotify_client_secret"] = v
	}
	if len(kv) > 0 {
		if err := config.SetSecrets(s.secretsPath, kv); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.spotifyStatus.Store(nil) // credentials changed, re-check on next view
		_ = s.reloadNow()
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) handleSpotifyAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	secrets, err := config.ReadSecretKeys(s.secretsPath, "spotify_client_id")
	if err != nil || secrets["spotify_client_id"] == "" {
		http.Redirect(w, r, "/settings?err=no_credentials", http.StatusFound)
		return
	}

	state, err := randomState()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.spotifyPendingState.Store(&spotifyPending{state: state, expires: time.Now().Add(5 * time.Minute)})

	url := s.spotify.AuthorizeURL(secrets["spotify_client_id"], spotifyRedirectURI(r), state, spotifyScope)
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) handleSpotifyCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Query().Get("error") != "" {
		http.Redirect(w, r, "/settings?err=denied", http.StatusFound)
		return
	}

	pending := s.spotifyPendingState.Load()
	s.spotifyPendingState.Store(nil) // one-shot: valid for at most one callback
	gotState := r.URL.Query().Get("state")
	if pending == nil || gotState == "" || gotState != pending.state || time.Now().After(pending.expires) {
		http.Redirect(w, r, "/settings?err=state_mismatch", http.StatusFound)
		return
	}

	secrets, err := config.ReadSecretKeys(s.secretsPath, "spotify_client_id", "spotify_client_secret")
	if err != nil || secrets["spotify_client_id"] == "" || secrets["spotify_client_secret"] == "" {
		http.Redirect(w, r, "/settings?err=no_credentials", http.StatusFound)
		return
	}
	creds := spotifyapi.Credentials{ClientID: secrets["spotify_client_id"], ClientSecret: secrets["spotify_client_secret"]}

	refreshToken, accessToken, _, err := s.spotify.ExchangeCode(r.Context(), creds, r.URL.Query().Get("code"), spotifyRedirectURI(r))
	if err != nil {
		http.Redirect(w, r, "/settings?err=exchange_failed", http.StatusFound)
		return
	}

	displayName, _ := s.spotify.WhoAmI(r.Context(), accessToken) // best-effort; blank is fine, checkSpotifyStatus retries later
	if err := config.SetSecrets(s.secretsPath, map[string]string{"spotify_refresh_token": refreshToken}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.spotifyStatus.Store(&spotifyStatus{displayName: displayName})
	_ = s.reloadNow()

	http.Redirect(w, r, "/settings", http.StatusFound)
}

func (s *Server) handleSpotifyDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	// Clears only the refresh token — client_id/secret (the app registration)
	// stay, so reconnecting doesn't require re-pasting them. This doesn't
	// revoke access on Spotify's side; that's done from the user's Spotify
	// account "Apps" page.
	if err := config.SetSecrets(s.secretsPath, map[string]string{"spotify_refresh_token": ""}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.spotifyStatus.Store(nil)
	_ = s.reloadNow()
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) handleSecretsSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	value := r.FormValue("value")

	switch {
	case !secretKeyRe.MatchString(key):
		http.Redirect(w, r, "/settings?serr=bad_key#secrets", http.StatusSeeOther)
		return
	case reservedSecretKeys[key]:
		http.Redirect(w, r, "/settings?serr=reserved_key#secrets", http.StatusSeeOther)
		return
	case value == "":
		http.Redirect(w, r, "/settings?serr=empty_value#secrets", http.StatusSeeOther)
		return
	}

	if err := config.SetSecrets(s.secretsPath, map[string]string{key: value}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.reloadNow()
	http.Redirect(w, r, "/settings#secrets", http.StatusSeeOther)
}

func (s *Server) handleSecretsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	if key != "" && !reservedSecretKeys[key] {
		if err := config.DeleteSecret(s.secretsPath, key); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = s.reloadNow()
	}
	http.Redirect(w, r, "/settings#secrets", http.StatusSeeOther)
}

func spotifyRedirectURI(r *http.Request) string {
	return "http://" + r.Host + "/settings/spotify/callback"
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// settingsVM is what settings.html renders. Sections drives both the
// left-hand nav and, in the same order, which cards appear in the main
// column — each sectionVM.ID matches a <section id="..."> the nav links to.
type settingsVM struct {
	Sections []sectionVM
	Spotify  spotifySettingsVM
	Secrets  secretsSettingsVM
}

type sectionVM struct {
	ID     string
	Label  string
	Status string
}

type secretsSettingsVM struct {
	Keys  []string
	Error string
}

func (sv secretsSettingsVM) StatusLine() string {
	if len(sv.Keys) == 1 {
		return "1 configured"
	}
	return fmt.Sprintf("%d configured", len(sv.Keys))
}

type spotifySettingsVM struct {
	ClientIDSet     bool
	ClientSecretSet bool
	Connected       bool
	DisplayName     string
	Error           string
	RedirectURI     string
	MediaSnippet    string
}

func (sp spotifySettingsVM) StatusLine() string {
	switch {
	case sp.Connected && sp.DisplayName != "":
		return "Connected as " + sp.DisplayName
	case sp.Connected:
		return "Connected"
	case sp.ClientIDSet && sp.ClientSecretSet:
		return "App registered — not connected"
	default:
		return "Not configured"
	}
}
