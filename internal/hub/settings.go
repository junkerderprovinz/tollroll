package hub

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// The levels from which an app may notify, by the severities of kit/notify.
// Critical is an alert, which goes out whatever the level.
const (
	LevelInfo     = "info"
	LevelWarning  = "warning"
	LevelFailure  = "failure"
	LevelCritical = "critical"
)

// Settings are what the hub does with a message. Apps holds the level of each
// app by its ID; an app without one sends everything. TakeOver offers the
// apps of the fortress to send their notifications through TollRoll.
type Settings struct {
	Quiet    Window            `json:"quiet"`
	Digest   Daily             `json:"digest"`
	Apps     map[string]string `json:"apps"`
	TakeOver bool              `json:"takeOver"`
}

// Window is a span of the day in the server's time, which may run past
// midnight.
type Window struct {
	On   bool   `json:"on"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Daily is a time of the day.
type Daily struct {
	On bool   `json:"on"`
	At string `json:"at"`
}

// ErrSettings is a setting that is out of shape.
var ErrSettings = errors.New("hub: invalid settings")

const (
	maxApps = 64
	maxID   = 60
)

var clock = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

var levels = map[string]int{LevelInfo: 0, LevelWarning: 1, LevelFailure: 2, LevelCritical: 3}

func defaults() Settings {
	return Settings{Quiet: Window{From: "22:00", To: "07:00"}, Digest: Daily{At: "08:00"}, Apps: map[string]string{}}
}

func (s Settings) check() error {
	if !clock.MatchString(s.Quiet.From) || !clock.MatchString(s.Quiet.To) || !clock.MatchString(s.Digest.At) || len(s.Apps) > maxApps {
		return ErrSettings
	}
	for id, l := range s.Apps {
		if _, ok := levels[l]; !ok || id == "" || len(id) > maxID {
			return ErrSettings
		}
	}
	return nil
}

// minutes is the time of the day hh:mm stands for.
func minutes(hhmm string) int {
	t, _ := time.Parse("15:04", hhmm)
	return t.Hour()*60 + t.Minute()
}

// quiet reports whether t falls in the quiet hours.
func (w Window) quiet(t time.Time) bool {
	if !w.On {
		return false
	}
	now, from, to := t.Hour()*60+t.Minute(), minutes(w.From), minutes(w.To)
	if from <= to {
		return now >= from && now < to
	}
	return now >= from || now < to
}

// Store keeps the settings in a file only root reads. It is safe for
// concurrent use.
type Store struct {
	path string

	mu sync.Mutex
	s  Settings
}

// OpenStore reads the settings at path. A missing or damaged file gives the
// defaults, with nothing switched on.
func OpenStore(path string) *Store {
	st := &Store{path: path, s: defaults()}
	var s Settings
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &s) == nil && s.check() == nil {
		if s.Apps == nil {
			s.Apps = map[string]string{}
		}
		st.s = s
	}
	return st
}

// Get returns the settings.
func (st *Store) Get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.s
	s.Apps = clone(st.s.Apps)
	return s
}

// Set saves s, or refuses it with ErrSettings and keeps the old ones.
func (st *Store) Set(s Settings) error {
	if s.Apps == nil {
		s.Apps = map[string]string{}
	}
	if err := s.check(); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
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
	st.s.Apps = clone(s.Apps)
	return nil
}

func clone(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
