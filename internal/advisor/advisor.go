// Package advisor looks at the server and the fortress and suggests the app
// of the house that would take care of something nothing takes care of yet,
// such as a backup app on a server where nothing backs up.
package advisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Facts are what the advisor goes by. Apps are the app IDs of the fortress,
// Plugins the plugins installed on this server and Containers the names of
// its containers. Backs says an app reports a backup it runs or has planned.
type Facts struct {
	Unraid     bool
	Apps       []string
	Plugins    []string
	Containers []string
	Backs      bool
}

// Suggestion is one app the advisor suggests. Text is the key of the
// sentence that says why.
type Suggestion struct {
	ID   string `json:"id"`
	App  string `json:"app"`
	Name string `json:"name"`
	Text string `json:"text"`
	Link string `json:"link"`
}

// rule suggests its app on a server where need holds.
type rule struct {
	Suggestion
	need func(Facts) bool
}

var rules = []rule{
	{Suggestion{ID: "backup", App: "bombvault", Name: "BombVault", Text: "a_backup"}, func(f Facts) bool { return !f.Backs }},
	{Suggestion{ID: "updates", App: "shiplog", Name: "ShipLog", Text: "a_updates"}, func(f Facts) bool { return len(f.Containers) > 0 }},
	{Suggestion{ID: "health", App: "firesquire", Name: "FireSquire", Text: "a_health"}, func(Facts) bool { return true }},
}

// Suggest lists what the advisor suggests on a server with f.
func Suggest(f Facts) []Suggestion {
	if !f.Unraid {
		return nil
	}
	var out []Suggestion
	for _, s := range rules {
		if s.need(f) && !f.has(s.App) {
			s.Link = "https://github.com/junkerderprovinz/" + s.App
			out = append(out, s.Suggestion)
		}
	}
	return out
}

// has reports whether app is in the fortress, installed as a plugin or runs
// in a container of a name that holds its own.
func (f Facts) has(app string) bool {
	if slices.Contains(f.Apps, app) || slices.Contains(f.Plugins, app) {
		return true
	}
	for _, c := range f.Containers {
		if strings.Contains(strings.ToLower(c), app) {
			return true
		}
	}
	return false
}

// Store keeps which suggestions were dismissed and whether the advisor has
// had its first look, in a file only root reads. It is safe for concurrent
// use.
type Store struct {
	path string

	mu sync.Mutex
	s  saved
}

type saved struct {
	Looked    bool     `json:"looked"`
	Dismissed []string `json:"dismissed"`
}

// ErrNoSuggestion is a dismissal of an ID the advisor does not know.
var ErrNoSuggestion = errors.New("advisor: no such suggestion")

// Open reads the store at path. A server without it has not been looked at.
func Open(path string) *Store {
	st := &Store{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &st.s)
	}
	return st
}

// List returns the suggestions for f that nobody dismissed.
func (st *Store) List(f Facts) []Suggestion {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := []Suggestion{}
	for _, s := range Suggest(f) {
		if !slices.Contains(st.s.Dismissed, s.ID) {
			out = append(out, s)
		}
	}
	return out
}

// Dismiss leaves the suggestion id out from now on.
func (st *Store) Dismiss(id string) error {
	if !slices.ContainsFunc(rules, func(r rule) bool { return r.ID == id }) {
		return ErrNoSuggestion
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if slices.Contains(st.s.Dismissed, id) {
		return nil
	}
	next := st.s
	next.Dismissed = append(slices.Clone(st.s.Dismissed), id)
	return st.save(next)
}

// First returns the suggestions for f once, on the first look at a server,
// so they can go into its log; later calls return none.
func (st *Store) First(f Facts) []Suggestion {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.s.Looked {
		return nil
	}
	next := st.s
	next.Looked = true
	if st.save(next) != nil {
		return nil
	}
	return Suggest(f)
}

// save writes s, called with mu held.
func (st *Store) save(s saved) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, st.path); err != nil {
		return err
	}
	st.s = s
	return nil
}
