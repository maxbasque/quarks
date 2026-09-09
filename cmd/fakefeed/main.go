// fakefeed is a development-only server that serves feeds shaped like the real
// ones (a YouTube per-channel Atom feed, a Radio-Canada-style news RSS feed, a
// Reddit-style Atom feed) with fresh timestamps on every request. Point quarks
// at it via config.fake.yaml to iterate on the UI without touching real APIs.
//
//	go run ./cmd/fakefeed        # serves on 127.0.0.1:7400
package main

import (
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"strings"
	"text/template"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7400", "listen address")
	flag.Parse()

	base := "http://" + *addr

	mux := http.NewServeMux()
	mux.HandleFunc("/youtube.xml", func(w http.ResponseWriter, r *http.Request) {
		writeFeed(w, youtubeTmpl, feedData(base, "UC_fake_channel_00000000", youtubeTitles, true))
	})
	mux.HandleFunc("/news.xml", func(w http.ResponseWriter, r *http.Request) {
		writeFeed(w, newsTmpl, feedData(base, "", newsTitles, false))
	})
	mux.HandleFunc("/reddit.xml", func(w http.ResponseWriter, r *http.Request) {
		writeFeed(w, redditTmpl, feedData(base, "", redditTitles, false))
	})
	mux.HandleFunc("/img/", handleImg)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "fakefeed — try %s/youtube.xml, /news.xml, /reddit.xml\n", base)
	})

	log.Printf("fakefeed on %s", base)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// ---- feed model -------------------------------------------------------------

type entry struct {
	ID        string
	VideoID   string
	Title     string
	Link      string
	Author    string
	Published string // RFC3339
	Updated   string
	Summary   string
	ThumbURL  string
	Comments  string
}

type feed struct {
	Base      string
	ChannelID string
	Updated   string
	Entries   []entry
}

func feedData(base, channelID string, titles []string, withThumbs bool) feed {
	now := time.Now()
	f := feed{Base: base, ChannelID: channelID, Updated: now.Format(time.RFC3339)}
	for i, title := range titles {
		// newest a few minutes old, then spreading roughly one item per ~40 min
		ts := now.Add(-time.Duration(i)*37*time.Minute - 4*time.Minute)
		slug := slugify(title)
		e := entry{
			ID:        fmt.Sprintf("fake:%s:%d", slug, i),
			VideoID:   fmt.Sprintf("vid%08d", hashInt(slug)),
			Title:     title,
			Link:      base + "/x/" + slug,
			Author:    authors[i%len(authors)],
			Published: ts.Format(time.RFC3339),
			Updated:   ts.Format(time.RFC3339),
			Summary:   "Placeholder summary for a fake item. " + title + ".",
			Comments:  base + "/x/" + slug + "#comments",
		}
		if withThumbs {
			e.ThumbURL = fmt.Sprintf("%s/img/%s", base, slug)
		}
		f.Entries = append(f.Entries, e)
	}
	return f
}

func writeFeed(w http.ResponseWriter, t *template.Template, f feed) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	if err := t.Execute(w, f); err != nil {
		log.Printf("template: %v", err)
	}
}

// ---- placeholder thumbnail -----------------------------------------------

func handleImg(w http.ResponseWriter, r *http.Request) {
	seed := strings.TrimPrefix(r.URL.Path, "/img/")
	hue := hashInt(seed) % 360
	w.Header().Set("Content-Type", "image/svg+xml")
	fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 480 360">`+
		`<rect width="480" height="360" fill="hsl(%d 45%% 50%%)"/>`+
		`<text x="240" y="195" font-family="sans-serif" font-size="40" fill="white" `+
		`text-anchor="middle" opacity="0.85">%s</text></svg>`, hue, "▶")
}

// ---- helpers ------------------------------------------------------------

func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func hashInt(s string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return int(h.Sum32() % 100000000)
}

// ---- content pools ---------------------------------------------------------

var authors = []string{"Alex Rivera", "Sam Okonkwo", "Priya Nair", "Jonas Berg", "Mei Lin"}

var youtubeTitles = []string{
	"I Built a Mechanical Keyboard From Scratch",
	"The Physics of Skipping Stones, Explained",
	"Why Old Synthesizers Sound Better (feat. a $12,000 Moog)",
	"Rewriting My Home Server for the Fourth Time",
	"Every Espresso Machine Under $500, Ranked",
	"How Bridges Actually Handle Wind",
	"A Weekend With the New RISC-V Laptop",
	"Restoring a 1974 Reel-to-Reel Tape Deck",
	"The Weird History of the QWERTY Layout",
	"Making Bread With a Sourdough Starter From 1998",
	"I Drove an EV Until It Died on the Highway",
	"What's Inside a Nuclear Density Gauge?",
	"The Lost Art of the Foldable Map",
	"Building a Telescope That Fits in a Backpack",
	"Can You Cool a Room With Just Physics?",
}

var newsTitles = []string{
	"City council approves expanded bike lane network for downtown core",
	"Provincial budget adds funding for rural broadband over three years",
	"Heat warning issued for the region as temperatures climb past 33 C",
	"Local hospital opens new emergency wing after two-year construction",
	"Transit agency proposes fare changes and a new night-bus service",
	"Farmers report strong harvest despite an unusually dry August",
	"University researchers publish study on lake water quality trends",
	"Bridge repairs to close one lane of the expressway for six weeks",
	"Museum acquires collection of early-20th-century photographs",
	"Housing starts rise for a second consecutive quarter, data shows",
	"Wildfire smoke prompts air quality advisory across the valley",
	"School board debates later start times for high school students",
	"Port authority reports record container traffic for the year",
	"New composting program to roll out to remaining neighbourhoods",
	"Regional election turnout up sharply from the previous cycle",
	"Power utility outlines plan to bury lines in storm-prone areas",
	"Snowfall totals break a decades-old record for the month",
	"Downtown library extends weekend hours after pilot succeeds",
	"Cyclist advocacy group calls for protected intersections",
	"Water main break floods a stretch of the market district",
}

var redditTitles = []string{
	"My self-hosted setup after 5 years — finally happy with it",
	"PSA: check your backup restores, not just your backups",
	"What are you all using for a read-later service in 2026?",
	"Migrated from Docker Compose to a single binary and don't regret it",
	"Weekly 'what did you deploy' thread",
	"Anyone else running their dashboard as a browser homepage?",
	"Cheap mini PC recommendations for a home server?",
	"I wrote a tiny RSS aggregator and it changed my mornings",
	"How do you handle secrets without a whole vault setup?",
	"Show and tell: my e-ink status display",
}

// ---- templates -----------------------------------------------------------

var funcs = template.FuncMap{}

var youtubeTmpl = template.Must(template.New("yt").Funcs(funcs).Parse(
	`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns:yt="http://www.youtube.com/xml/schemas/2015"
      xmlns:media="http://search.yahoo.com/mrss/"
      xmlns="http://www.w3.org/2005/Atom">
  <yt:channelId>{{ .ChannelID }}</yt:channelId>
  <title>Fake Channel</title>
  <link rel="alternate" href="{{ .Base }}/x/channel"/>
  <author><name>Fake Channel</name><uri>{{ .Base }}/x/channel</uri></author>
  <updated>{{ .Updated }}</updated>
  {{- range .Entries }}
  <entry>
    <id>yt:video:{{ .VideoID }}</id>
    <yt:videoId>{{ .VideoID }}</yt:videoId>
    <yt:channelId>{{ $.ChannelID }}</yt:channelId>
    <title>{{ .Title }}</title>
    <link rel="alternate" href="{{ .Link }}"/>
    <author><name>{{ .Author }}</name></author>
    <published>{{ .Published }}</published>
    <updated>{{ .Updated }}</updated>
    <media:group>
      <media:title>{{ .Title }}</media:title>
      <media:thumbnail url="{{ .ThumbURL }}" width="480" height="360"/>
      <media:description>{{ .Summary }}</media:description>
    </media:group>
  </entry>
  {{- end }}
</feed>
`))

var newsTmpl = template.Must(template.New("news").Funcs(funcs).Parse(
	`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Fake News — Regional</title>
    <link>{{ .Base }}/x/news</link>
    <description>Placeholder regional news feed for development.</description>
    <lastBuildDate>{{ .Updated }}</lastBuildDate>
    {{- range .Entries }}
    <item>
      <title>{{ .Title }}</title>
      <link>{{ .Link }}</link>
      <guid isPermaLink="false">{{ .ID }}</guid>
      <dc:creator xmlns:dc="http://purl.org/dc/elements/1.1/">{{ .Author }}</dc:creator>
      <pubDate>{{ .Published }}</pubDate>
      <description>{{ .Summary }}</description>
    </item>
    {{- end }}
  </channel>
</rss>
`))

var redditTmpl = template.Must(template.New("reddit").Funcs(funcs).Parse(
	`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>fake home feed</title>
  <link rel="alternate" href="{{ .Base }}/x/reddit"/>
  <updated>{{ .Updated }}</updated>
  {{- range .Entries }}
  <entry>
    <id>{{ .ID }}</id>
    <title>{{ .Title }}</title>
    <link rel="alternate" href="{{ .Link }}"/>
    <author><name>u/{{ .Author }}</name></author>
    <published>{{ .Published }}</published>
    <updated>{{ .Updated }}</updated>
    <content type="html">&lt;a href="{{ .Comments }}"&gt;comments&lt;/a&gt;</content>
  </entry>
  {{- end }}
</feed>
`))
