package core

// Standings is the payload for a sports-standings widget: one or more grouped
// tables (by division / conference / league). Like Weather, it does not fit the
// Item shape and gets its own renderer.
type Standings struct {
	Groups []StandingsGroup
}

type StandingsGroup struct {
	Name    string   // "Atlantic", "AL East", … ("" for a single flat table)
	Columns []string // right-aligned stat headers, parallel to each row's Values
	Rows    []StandingsRow
}

type StandingsRow struct {
	Rank      int
	Team      string
	Abbrev    string
	Logo      string
	Values    []string // parallel to the group's Columns
	Highlight bool     // e.g. the configured favourite team
}
