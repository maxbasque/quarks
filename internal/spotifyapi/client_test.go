package spotifyapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(authSrv, apiSrv *httptest.Server) *Client {
	c := NewClient()
	if authSrv != nil {
		c.AuthBase = authSrv.URL
	}
	if apiSrv != nil {
		c.APIBase = apiSrv.URL
	}
	return c
}

func TestAuthorizeURL(t *testing.T) {
	c := NewClient()
	c.AuthBase = "https://accounts.spotify.com"
	got := c.AuthorizeURL("cid", "http://127.0.0.1:7373/settings/spotify/callback", "xyz", "user-follow-read")
	if !strings.HasPrefix(got, "https://accounts.spotify.com/authorize?") {
		t.Fatalf("unexpected authorize URL: %s", got)
	}
	for _, want := range []string{"client_id=cid", "response_type=code", "state=xyz", "scope=user-follow-read"} {
		if !strings.Contains(got, want) {
			t.Errorf("authorize URL missing %q: %s", want, got)
		}
	}
}

func TestExchangeCode(t *testing.T) {
	var gotForm string
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		gotForm = r.Form.Encode()
		gotAuthHeader = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "at-1",
			"refresh_token": "rt-1",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()

	c := testClient(srv, nil)
	creds := Credentials{ClientID: "cid", ClientSecret: "csecret"}
	rt, at, exp, err := c.ExchangeCode(context.Background(), creds, "authcode", "http://cb")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if rt != "rt-1" || at != "at-1" || exp != 3600 {
		t.Errorf("got rt=%q at=%q exp=%d", rt, at, exp)
	}
	if !strings.HasPrefix(gotAuthHeader, "Basic ") {
		t.Errorf("expected Basic auth, got %q", gotAuthHeader)
	}
	if !strings.Contains(gotForm, "grant_type=authorization_code") || !strings.Contains(gotForm, "code=authcode") {
		t.Errorf("unexpected form: %s", gotForm)
	}
}

func TestExchangeCodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	c := testClient(srv, nil)
	_, _, _, err := c.ExchangeCode(context.Background(), Credentials{}, "bad", "http://cb")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestRefreshAccessTokenDoesNotRequireNewRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-2",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	c := testClient(srv, nil)
	at, exp, err := c.RefreshAccessToken(context.Background(), Credentials{RefreshToken: "rt-1"})
	if err != nil {
		t.Fatalf("RefreshAccessToken: %v", err)
	}
	if at != "at-2" || exp != 3600 {
		t.Errorf("got at=%q exp=%d", at, exp)
	}
}

func TestWhoAmI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/me" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer at-1" {
			t.Errorf("missing bearer token: %s", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"display_name": "Max"})
	}))
	defer srv.Close()

	c := testClient(nil, srv)
	name, err := c.WhoAmI(context.Background(), "at-1")
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if name != "Max" {
		t.Errorf("got %q", name)
	}
}

func TestFollowedArtistsPaginates(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		after := r.URL.Query().Get("after")
		switch after {
		case "":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"artists": map[string]any{
					"items":   []map[string]any{{"id": "a1", "name": "Artist One"}},
					"cursors": map[string]any{"after": "a1"},
				},
			})
		case "a1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"artists": map[string]any{
					"items":   []map[string]any{{"id": "a2", "name": "Artist Two"}},
					"cursors": map[string]any{"after": ""},
				},
			})
		default:
			t.Fatalf("unexpected after=%q", after)
		}
	}))
	defer srv.Close()

	c := testClient(nil, srv)
	artists1, next1, err := c.FollowedArtists(context.Background(), "at-1", "")
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(artists1) != 1 || artists1[0].Name != "Artist One" || next1 != "a1" {
		t.Fatalf("page 1 = %+v next=%q", artists1, next1)
	}

	artists2, next2, err := c.FollowedArtists(context.Background(), "at-1", next1)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(artists2) != 1 || artists2[0].Name != "Artist Two" || next2 != "" {
		t.Fatalf("page 2 = %+v next=%q", artists2, next2)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestFollowedArtistsReturnsLong429AsRateLimitedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "74442")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, _, err := testClient(nil, srv).FollowedArtists(context.Background(), "at-1", "")
	var rl *RateLimitedError
	if !errors.As(err, &rl) || rl.RetryAfter != 74442*time.Second {
		t.Fatalf("want a *RateLimitedError carrying Retry-After, got %v", err)
	}
}

func TestDoGETWraps401AsErrUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := testClient(nil, srv).WhoAmI(context.Background(), "expired")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("want ErrUnauthorized, got %v", err)
	}
}
