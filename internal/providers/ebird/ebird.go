// Package ebird lists recent "notable" bird sightings — locally rare or
// out-of-season species — from the eBird API. Needs a free API token
// (ebird.org/api/keygen) and either a region code ("CA-QC") or a lat/lng.
package ebird

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/maxbasque/quarks/internal/core"
)

const userAgent = "quarks/0.1 (+https://github.com/maxbasque/quarks)"

type settings struct {
	Token  string  `yaml:"token"`  // eBird API token, usually "${secret:ebird_key}"
	Region string  `yaml:"region"` // eBird region code, e.g. CA-QC (takes precedence)
	Lat    float64 `yaml:"lat"`    // used when region is empty
	Lng    float64 `yaml:"lng"`    //
	Dist   int     `yaml:"dist"`   // search radius in km for lat/lng, default 25
	Back   int     `yaml:"back"`   // how many days back to look, default 7
}

type Provider struct {
	token  string
	region string
	lat    float64
	lng    float64
	dist   int
	back   int
	limit  int
	base   string // overridable for tests
	http   *http.Client
}

// New is the core.Factory for "ebird".
func New(cfg core.WidgetConfig) (core.Provider, error) {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return nil, err
	}
	s.Region = strings.ToUpper(strings.TrimSpace(s.Region))
	if s.Region == "" && s.Lat == 0 && s.Lng == 0 {
		return nil, fmt.Errorf("ebird widget %q: set a region (e.g. CA-QC) or lat/lng", cfg.Title)
	}
	if s.Dist <= 0 {
		s.Dist = 25
	}
	if s.Back <= 0 {
		s.Back = 7
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = 15
	}
	return &Provider{
		token:  strings.TrimSpace(s.Token),
		region: s.Region,
		lat:    s.Lat,
		lng:    s.Lng,
		dist:   s.Dist,
		back:   s.Back,
		limit:  limit,
		base:   "https://api.ebird.org/v2",
		http:   &http.Client{},
	}, nil
}

type observation struct {
	SpeciesCode string `json:"speciesCode"`
	ComName     string `json:"comName"`
	SciName     string `json:"sciName"`
	LocName     string `json:"locName"`
	ObsDt       string `json:"obsDt"`
	HowMany     int    `json:"howMany"`
	SubID       string `json:"subId"`
	UserName    string `json:"userDisplayName"`
	Sub1Name    string `json:"subnational1Name"`
	Sub2Name    string `json:"subnational2Name"`
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	if p.token == "" {
		return core.Payload{}, fmt.Errorf("no eBird token — add ebird_key to secrets.yaml (get one at ebird.org/api/keygen)")
	}

	q := url.Values{}
	q.Set("detail", "full")
	q.Set("back", strconv.Itoa(p.back))
	q.Set("maxResults", strconv.Itoa(p.limit))

	var endpoint string
	if p.region != "" {
		endpoint = fmt.Sprintf("%s/data/obs/%s/recent/notable", p.base, url.PathEscape(p.region))
	} else {
		endpoint = p.base + "/data/obs/geo/recent/notable"
		q.Set("lat", strconv.FormatFloat(p.lat, 'f', 2, 64))
		q.Set("lng", strconv.FormatFloat(p.lng, 'f', 2, 64))
		q.Set("dist", strconv.Itoa(p.dist))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return core.Payload{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-eBirdApiToken", p.token)

	resp, err := p.http.Do(req)
	if err != nil {
		return core.Payload{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return core.Payload{}, fmt.Errorf("eBird rejected the token (http %d) — check ebird_key in secrets.yaml", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return core.Payload{}, fmt.Errorf("ebird: http %d", resp.StatusCode)
	}

	var obs []observation
	if err := json.NewDecoder(resp.Body).Decode(&obs); err != nil {
		return core.Payload{}, err
	}

	items := make([]core.Item, 0, len(obs))
	for _, o := range obs {
		if o.ComName == "" {
			continue
		}
		title := o.ComName
		if o.HowMany > 1 {
			title = fmt.Sprintf("%s (%d)", o.ComName, o.HowMany)
		}
		it := core.Item{
			ID:          o.SubID + "-" + o.SpeciesCode,
			Title:       title,
			URL:         "https://ebird.org/checklist/" + o.SubID,
			Source:      o.LocName,
			Author:      o.UserName,
			PublishedAt: parseObsDt(o.ObsDt),
			Summary:     strings.TrimSpace(strings.Join(nonEmpty(o.SciName, place(o)), " · ")),
		}
		items = append(items, it)
	}
	return core.Feed(items), nil
}

// parseObsDt handles both "2006-01-02 15:04" and the date-only "2006-01-02" that
// eBird returns when the observer logged no start time. Times are local to the
// sighting; we read them in the server's location, which is close enough.
func parseObsDt(s string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(s), time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

func place(o observation) string {
	if o.Sub2Name != "" {
		return o.Sub2Name
	}
	return o.Sub1Name
}

func nonEmpty(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}
