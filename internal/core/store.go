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
	Key      string    `json:"key"`
	Title    string    `json:"title"`
	Column   int       `json:"column"`
	Type     string    `json:"type"`
	Items    []Item    `json:"items"`
	LastOK   time.Time `json:"last_ok"`   // zero until a fetch succeeds
	LastErr  string    `json:"last_err"`  // last fetch error, "" if last fetch was ok
	LastTry  time.Time `json:"last_try"`  //
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

// Register seeds a widget's state, loading a disk snapshot if one exists.
func (s *Store) Register(key, title string, column int, typ string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := &WidgetState{Key: key, Title: title, Column: column, Type: typ}
	if data, err := os.ReadFile(s.path(key)); err == nil {
		_ = json.Unmarshal(data, st)
		// config wins for identity fields
		st.Key, st.Title, st.Column, st.Type = key, title, column, typ
	}
	s.states[key] = st
}

// SetItems records a successful fetch and writes the snapshot.
func (s *Store) SetItems(key string, items []Item) {
	s.mu.Lock()
	st := s.states[key]
	if st == nil {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	st.Items = items
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
