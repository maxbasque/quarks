// Package spotifyapi is a thin, dependency-free client for the pieces of
// Spotify's accounts/Web API this app needs: the confidential-client
// Authorization Code OAuth flow, the current user's identity, and their
// followed artists. Release data comes from MusicBrainz instead (see
// internal/musicbrainz): Spotify's catalog rarely lists a record before it's
// out, and polling it per artist is what got this app rate limited. It knows nothing about core.Provider or
// the HTTP settings handlers — both depend on this package, not the other way
// around, so it stays independently testable.
package spotifyapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Credentials is what's needed to talk to Spotify on a user's behalf: the app
// registration (ClientID/ClientSecret, from the Spotify Developer dashboard)
// plus that user's long-lived refresh token from a completed Authorization
// Code exchange.
type Credentials struct {
	ClientID     string
	ClientSecret string
	RefreshToken string
}

// Artist is a followed artist, as returned by FollowedArtists.
type Artist struct {
	ID   string
	Name string
}

// Client talks to Spotify's accounts + Web API. AuthBase/APIBase are
// overridable so tests can point them at a local fixture server.
type Client struct {
	HTTP     *http.Client
	AuthBase string
	APIBase  string
}

func NewClient() *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 15 * time.Second},
		AuthBase: "https://accounts.spotify.com",
		APIBase:  "https://api.spotify.com/v1",
	}
}

// AuthorizeURL builds the URL to send the user's browser to for consent.
func (c *Client) AuthorizeURL(clientID, redirectURI, state, scope string) string {
	q := url.Values{
		"client_id":     {clientID},
		"response_type": {"code"},
		"redirect_uri":  {redirectURI},
		"state":         {state},
		"scope":         {scope},
	}
	return c.AuthBase + "/authorize?" + q.Encode()
}

// ExchangeCode redeems an authorization code (from the callback's ?code=) for
// a refresh token and an initial access token.
func (c *Client) ExchangeCode(ctx context.Context, creds Credentials, code, redirectURI string) (refreshToken, accessToken string, expiresIn int, err error) {
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
	}
	return c.token(ctx, creds, form)
}

// RefreshAccessToken mints a new short-lived access token from creds'
// RefreshToken. On this confidential-client flow Spotify does not rotate the
// refresh token, so the caller's stored token stays valid unless the returned
// refreshToken is non-empty (Spotify occasionally does still send one; callers
// should persist it if so, but in practice it repeats the same value).
func (c *Client) RefreshAccessToken(ctx context.Context, creds Credentials) (accessToken string, expiresIn int, err error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {creds.RefreshToken},
	}
	_, accessToken, expiresIn, err = c.token(ctx, creds, form)
	return accessToken, expiresIn, err
}

func (c *Client) token(ctx context.Context, creds Credentials, form url.Values) (refreshToken, accessToken string, expiresIn int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.AuthBase+"/api/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(creds.ClientID, creds.ClientSecret)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", "", 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", 0, fmt.Errorf("spotify token: http %d: %s", resp.StatusCode, trim(body))
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", "", 0, fmt.Errorf("spotify token: parse response: %w", err)
	}
	return tr.RefreshToken, tr.AccessToken, tr.ExpiresIn, nil
}

// WhoAmI returns the display name of the account an access token belongs to.
func (c *Client) WhoAmI(ctx context.Context, accessToken string) (string, error) {
	var me struct {
		DisplayName string `json:"display_name"`
	}
	if err := c.getJSON(ctx, accessToken, c.APIBase+"/me", &me); err != nil {
		return "", err
	}
	return me.DisplayName, nil
}

// FollowedArtists returns one page of the user's followed artists. Pass an
// empty after for the first page, then the returned next cursor until it
// comes back empty.
func (c *Client) FollowedArtists(ctx context.Context, accessToken, after string) (artists []Artist, next string, err error) {
	q := url.Values{"type": {"artist"}, "limit": {"50"}}
	if after != "" {
		q.Set("after", after)
	}
	var page struct {
		Artists struct {
			Items []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"items"`
			Cursors struct {
				After string `json:"after"`
			} `json:"cursors"`
		} `json:"artists"`
	}
	if err := c.getJSON(ctx, accessToken, c.APIBase+"/me/following?"+q.Encode(), &page); err != nil {
		return nil, "", err
	}
	for _, a := range page.Artists.Items {
		artists = append(artists, Artist{ID: a.ID, Name: a.Name})
	}
	return artists, page.Artists.Cursors.After, nil
}

// getJSON does an authenticated GET. There's deliberately no inline retry:
// a 429 comes back as a *RateLimitedError for the caller to honor across
// polls (see the provider's blockedUntil).
func (c *Client) getJSON(ctx context.Context, accessToken, endpoint string, v any) error {
	return c.doGET(ctx, accessToken, endpoint, v)
}

// RateLimitedError is a 429 from the Web API. RetryAfter is Spotify's own
// Retry-After — which can be many hours once an app has been flagged, not
// just the few seconds of the rolling-window limit.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("spotify: rate limited, retry after %s", e.RetryAfter)
}

// ErrUnauthorized wraps a 401 from the Web API — the access token was
// rejected (expired or revoked), so the caller should drop any cached one.
var ErrUnauthorized = errors.New("spotify: access token rejected")

func (c *Client) doGET(ctx context.Context, accessToken, endpoint string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
		if err != nil || secs < 0 {
			secs = 5 // no usable Retry-After header — a conservative guess
		}
		return &RateLimitedError{RetryAfter: time.Duration(secs) * time.Second}
	}

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w: http 401 on %s: %s", ErrUnauthorized, endpoint, trim(body))
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("spotify: http %d on %s: %s", resp.StatusCode, endpoint, trim(body))
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("spotify: parse response from %s: %w", endpoint, err)
	}
	return nil
}

func trim(b []byte) string {
	const max = 300
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
