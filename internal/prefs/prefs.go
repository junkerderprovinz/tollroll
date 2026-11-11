// Package prefs keeps how the tile looks on this server, so every browser
// that opens the dashboard finds it the way it was left.
package prefs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// The views of the tile: rows and log, the rows alone, the log alone, or the
// map of the fortress.
const (
	ViewAll  = "all"
	ViewApps = "apps"
	ViewLog  = "log"
	ViewMap  = "map"
)

// ErrView is a view the tile does not have.
var ErrView = errors.New("prefs: no such view")

// Prefs is what the tile remembers.
type Prefs struct {
	View string `json:"view"`
}

// Store holds the prefs and writes every change to its file. It is safe for
// concurrent use.
type Store struct {
	path string

	mu sync.Mutex
	p  Prefs
}

// Open reads the prefs from path. A missing or damaged file gives the tile
// with everything on it, since a lost view is no reason to stop the service.
func Open(path string) *Store {
	s := &Store{path: path, p: Prefs{View: ViewAll}}
	var p Prefs
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &p) == nil && valid(p) {
		s.p = p
	}
	return s
}

// Get returns the prefs.
func (s *Store) Get() Prefs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.p
}

// Set saves p, or refuses it with ErrView and keeps the old prefs.
func (s *Store) Set(p Prefs) error {
	if !valid(p) {
		return ErrView
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.p = p
	return nil
}

func valid(p Prefs) bool {
	return p.View == ViewAll || p.View == ViewApps || p.View == ViewLog || p.View == ViewMap
}
