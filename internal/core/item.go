package core

import "time"

// Item is the normalized unit rendered on screen. Every feed-type provider
// returns a slice of these. Weather and calendar are deliberate exceptions and
// get their own types (see the plan, §6).
type Item struct {
	ID          string    // stable, for dedupe + "seen" tracking
	Title       string    //
	URL         string    // where a click goes
	Source      string    // "Hacker News", "r/selfhosted", "Radio-Canada"
	Author      string    //
	PublishedAt time.Time //
	Thumbnail   string    //
	Score       int       // upvotes / HN points; 0 if N/A
	Comments    int       //
	CommentsURL string    // discussion link, distinct from URL
	Body        string    // populated lazily by the reader view, not on poll
}
