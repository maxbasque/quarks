package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// WidgetState is everything the UI needs to render one widget: the last-good
// items plus freshness metadata. The UI never blocks on the network — it renders
// whatever is here.
type WidgetState struct {
	Key     string    `json:"key"`
	Title   string    `json:"title"`
	Order   int       `json:"order"`    // position in the config, for stable UI ordering
	Column  int       `json:"column"`   //
	Type    string    `json:"type"`     //
	Items   []Item    `json:"items"`    // feed widgets
	Weather *Weather  `json:"weather"`  // weather widgets
	LastOK  time.Time `json:"last_ok"`  // zero until a fetch succeeds
	LastErr string    `json:"last_err"` // last fetch error, "" if last fetch was ok
	LastTry time.Time `json:"last_try"` //
}

// Stale reports whether the newest good data is older than ttl.
func (s WidgetState) Stale(ttl time.Duration) bool {
	return !s.LastOK.IsZero() && time.Since(s.LastOK) > ttl
}

// Store holds widget state in memory and mirrors each widget to a JSON file in
// cacheDir so a cold start is populated instantly instead of blank.
type Store struct {
	mu       sync.RWMutex
	cacheDir string
	states   map[string]*WidgetState
}

func NewStore(cacheDir string) (*Store, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	return &Store{
		cacheDir: cacheDir,
		states:   make(map[string]*WidgetState),
	}, nil
}

// Register seeds a widget's state. On first sight it loads any disk snapshot; on
// a config reload it keeps the in-memory items and just refreshes the identity
// fields, so a reload never blanks the dashboard.
func (s *Store) Register(key, title string, order, column int, typ string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if st, ok := s.states[key]; ok {
		st.Title, st.Order, st.Column, st.Type = title, order, column, typ
		return
	}

	st := &WidgetState{Key: key, Title: title, Order: order, Column: column, Type: typ}
	if data, err := os.ReadFile(s.path(key)); err == nil {
		_ = json.Unmarshal(data, st)
		st.Key, st.Title, st.Order, st.Column, st.Type = key, title, order, column, typ
	}
	s.states[key] = st
}

// Retain drops in-memory state (and the disk snapshot) for any widget whose key
// is not in keep — i.e. widgets removed from the config on reload.
func (s *Store) Retain(keep map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k := range s.states {
		if !keep[k] {
			delete(s.states, k)
			_ = os.Remove(s.path(k))
		}
	}
}

// SetPayload records a successful fetch and writes the snapshot.
func (s *Store) SetPayload(key string, p Payload) {
	s.mu.Lock()
	st := s.states[key]
	if st == nil {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	st.Items = p.Items
	st.Weather = p.Weather
	st.LastOK = now
	st.LastTry = now
	st.LastErr = ""
	snapshot := *st
	s.mu.Unlock()

	s.persist(key, snapshot)
}

// SetError records a failed fetch. Existing items are kept.
func (s *Store) SetError(key string, err error) {
	s.mu.Lock()
	st := s.states[key]
	if st == nil {
		s.mu.Unlock()
		return
	}
	st.LastTry = time.Now()
	st.LastErr = err.Error()
	snapshot := *st
	s.mu.Unlock()

	s.persist(key, snapshot)
}

// Snapshot returns a copy of all widget states.
func (s *Store) Snapshot() []WidgetState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]WidgetState, 0, len(s.states))
	for _, st := range s.states {
		out = append(out, *st)
	}
	return out
}

func (s *Store) persist(key string, st WidgetState) {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	tmp := s.path(key) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, s.path(key))
}

func (s *Store) path(key string) string {
	return filepath.Join(s.cacheDir, key+".json")
}
