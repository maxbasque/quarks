// Package potd shows Wikimedia Commons' "Picture of the Day" — one large image
// with its caption and credit. It reads the keyless REST "featured" feed (same
// endpoint family as onthisday).
package potd

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/maxbasque/quarks/internal/core"
)

const userAgent = "quarks/0.1 (+https://github.com/maxbasque/quarks)"

// thumbWidth is the width we ask Wikimedia to render the lead image at. The feed
// hands back a ~640px thumbnail URL; bumping the width keeps it crisp on a wide
// screen without ever pulling the multi-megabyte original.
const thumbWidth = 1280

type settings struct {
	Lang string `yaml:"lang"` // caption language / wiki, default "fr"
}

type Provider struct {
	lang string
	base string           // overridable for tests
	now  func() time.Time // overridable for tests
	http *http.Client
}

// New is the core.Factory for "potd".
func New(cfg core.WidgetConfig) (core.Provider, error) {
	var s settings
	if err := cfg.Decode(&s); err != nil {
		return nil, err
	}
	lang := strings.ToLower(strings.TrimSpace(s.Lang))
	if lang == "" {
		lang = "fr"
	}
	return &Provider{
		lang: lang,
		base: "https://%s.wikipedia.org/api/rest_v1/feed/featured",
		now:  time.Now,
		http: &http.Client{},
	}, nil
}

type featured struct {
	Image *image `json:"image"`
}

type image struct {
	Title     string `json:"title"`
	FilePage  string `json:"file_page"`
	Thumbnail struct {
		Source string `json:"source"`
	} `json:"thumbnail"`
	Artist struct {
		Text string `json:"text"`
	} `json:"artist"`
	Credit struct {
		Text string `json:"text"`
	} `json:"credit"`
	License struct {
		Type string `json:"type"`
	} `json:"license"`
	Description struct {
		Text string `json:"text"`
		Lang string `json:"lang"`
	} `json:"description"`
	Structured struct {
		Captions map[string]string `json:"captions"`
	} `json:"structured"`
}

func (p *Provider) Fetch(ctx context.Context) (core.Payload, error) {
	// Today's picture may not be posted yet, and a server clock ahead of UTC can
	// ask for a date the feed rejects — fall back to yesterday once.
	day := p.now()
	img, err := p.fetchDay(ctx, day)
	if err != nil {
		if alt, altErr := p.fetchDay(ctx, day.AddDate(0, 0, -1)); altErr == nil {
			img = alt
		} else {
			return core.Payload{}, err
		}
	}

	caption := p.caption(img)
	title := caption
	if title == "" {
		title = cleanFileTitle(img.Title)
	}

	it := core.Item{
		ID:        img.Title,
		Title:     title,
		URL:       img.FilePage,
		Source:    "Wikimedia Commons",
		Author:    stripTags(firstNonEmpty(img.Artist.Text, img.Credit.Text)),
		Thumbnail: upscale(img.Thumbnail.Source),
		Hero:      true,
	}
	if lic := strings.TrimSpace(img.License.Type); lic != "" {
		if it.Author != "" {
			it.Author += " · " + lic
		} else {
			it.Author = lic
		}
	}
	if caption != "" && caption != title {
		it.Summary = caption
	}
	return core.Feed([]core.Item{it}), nil
}

func (p *Provider) fetchDay(ctx context.Context, d time.Time) (*image, error) {
	url := fmt.Sprintf(p.base, p.lang) + d.Format("/2006/01/02")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wikimedia featured: http %d", resp.StatusCode)
	}

	var data featured
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if data.Image == nil || data.Image.Thumbnail.Source == "" {
		return nil, fmt.Errorf("no picture of the day for %s", d.Format("2006-01-02"))
	}
	return data.Image, nil
}

// caption picks the best available description: a structured caption in the
// widget's language, then English, then the free-text description.
func (p *Provider) caption(img *image) string {
	if c := strings.TrimSpace(img.Structured.Captions[p.lang]); c != "" {
		return clip(c, 240)
	}
	if c := strings.TrimSpace(img.Structured.Captions["en"]); c != "" {
		return clip(c, 240)
	}
	return clip(img.Description.Text, 240)
}

var (
	wsRe    = regexp.MustCompile(`\s+`)
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	widthRe = regexp.MustCompile(`/(\d+)px-`)
	extRe   = regexp.MustCompile(`\.[A-Za-z0-9]+$`)
)

// upscale rewrites a Wikimedia thumbnail URL to a wider render. Wikimedia serves
// arbitrary widths on demand and never upscales past the original, so a no-match
// or an already-small original just falls through unchanged.
func upscale(src string) string {
	return widthRe.ReplaceAllString(src, fmt.Sprintf("/%dpx-", thumbWidth))
}

func cleanFileTitle(s string) string {
	s = strings.TrimPrefix(s, "File:")
	s = extRe.ReplaceAllString(s, "")
	return strings.TrimSpace(strings.ReplaceAll(s, "_", " "))
}

func stripTags(s string) string {
	return strings.TrimSpace(wsRe.ReplaceAllString(html.UnescapeString(tagRe.ReplaceAllString(s, "")), " "))
}

func clip(s string, max int) string {
	s = stripTags(s)
	if len(s) <= max {
		return s
	}
	if i := strings.LastIndex(s[:max], " "); i > 0 {
		s = s[:i]
	} else {
		s = s[:max]
	}
	return s + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
