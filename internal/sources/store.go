// Package sources keeps the list of apps the tile reads: where each one
// answers and the read token it needs. Tokens stay in this package and in the
// file on the flash; everything that leaves for the browser goes through
// Public, which carries none.
package sources

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// MaxSources is how many apps one tile reads.
const MaxSources = 32

// Kind says how a source is reached.
type Kind string

const (
	// KindURL is an app reached over HTTP(S) with a read token.
	KindURL Kind = "url"
	// KindSocket is an app on this server that answers on a unix socket,
	// where reaching the socket is the permission.
	KindSocket Kind = "socket"
)

// Source is one app as the service reads it. Pin is the SHA-256 of the
// certificate the app showed the first time, for apps whose certificate no
// public authority vouches for.
type Source struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	Token   string `json:"token,omitempty"`
	Pin     string `json:"pin,omitempty"`
}

// Kind tells a socket path from a web address.
func (s Source) Kind() Kind {
	if !strings.Contains(s.Address, "://") {
		return KindSocket
	}
	return KindURL
}

// Pinned reports whether a certificate has been learned for the source.
func (s Source) Pinned() bool { return s.Pin != "" }

// Public is a source as the settings page sees it.
type Public struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	Kind     Kind   `json:"kind"`
	HasToken bool   `json:"hasToken"`
	Pinned   bool   `json:"pinned"`
}

// Edit is one row of the settings page. An empty ID adds a source. An empty
// Token keeps the saved one, as long as the address stays the same. Trust forgets the learned certificate, so the
// next contact learns the one the app shows now.
type Edit struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	Token   string `json:"token"`
	Trust   bool   `json:"trust"`
}

// AddressError refuses a list of edits and names the row at fault. Code is
// one of invalid_address, token_missing, duplicate, unknown_source and
// too_many.
type AddressError struct {
	Index int
	Code  string
}

func (e *AddressError) Error() string {
	return fmt.Sprintf("source %d: %s", e.Index, e.Code)
}

type file struct {
	Sources []Source `json:"sources"`
}

// Store holds the sources and writes every change to its file at once.
type Store struct {
	mu      sync.Mutex
	path    string
	sources []Source
}

// Open reads the sources from path. Without that file, it takes the address
// and token of the old single-app widget from legacy, if there are any, and
// writes the file, so the old settings are taken over once and never again.
func Open(path, legacy string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		var f file
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		s.sources = f.Sources
		return s, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	if src, ok := fromWidget(legacy); ok {
		s.sources = []Source{src}
		if err := s.save(s.sources); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// List returns the sources with their tokens, for the service only.
func (s *Store) List() []Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Source(nil), s.sources...)
}

// Public returns the sources without their tokens.
func (s *Store) Public() []Public {
	s.mu.Lock()
	defer s.mu.Unlock()
	return public(s.sources)
}

// Replace makes the edits the new list of sources. Either every edit is
// valid and the list is saved, or nothing changes and the error is an
// *AddressError.
func (s *Store) Replace(edits []Edit) ([]Public, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(edits) > MaxSources {
		return nil, &AddressError{Index: MaxSources, Code: "too_many"}
	}
	old := map[string]Source{}
	for _, src := range s.sources {
		old[src.ID] = src
	}
	seen := map[string]bool{}
	next := make([]Source, 0, len(edits))
	for i, e := range edits {
		addr, ok := normalize(e.Address)
		if !ok {
			return nil, &AddressError{Index: i, Code: "invalid_address"}
		}
		if seen[addr] {
			return nil, &AddressError{Index: i, Code: "duplicate"}
		}
		seen[addr] = true
		src := Source{ID: e.ID, Address: addr}
		if e.ID == "" {
			src.ID = newID()
		} else {
			prev, ok := old[e.ID]
			if !ok {
				return nil, &AddressError{Index: i, Code: "unknown_source"}
			}
			// The saved token and certificate belong to the saved address and
			// never go along to a new one.
			if prev.Address == addr {
				src.Token = prev.Token
				if !e.Trust {
					src.Pin = prev.Pin
				}
			}
		}
		if t := strings.TrimSpace(e.Token); t != "" {
			src.Token = t
		}
		if src.Kind() == KindSocket {
			src.Token, src.Pin = "", ""
		} else if src.Token == "" {
			return nil, &AddressError{Index: i, Code: "token_missing"}
		}
		next = append(next, src)
	}
	if err := s.save(next); err != nil {
		return nil, err
	}
	s.sources = next
	return public(next), nil
}

// Learn keeps pin as the certificate of the source with id, unless one has
// been learned already or the source has moved to another address since the
// read began. A failed write leaves the pin in memory, so it holds until the
// next restart.
func (s *Store) Learn(id, address, pin string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := append([]Source(nil), s.sources...)
	for i := range next {
		if next[i].ID == id && next[i].Address == address && next[i].Pin == "" && next[i].Kind() == KindURL {
			next[i].Pin = pin
			s.sources = next
			_ = s.save(next)
			return
		}
	}
}

// Probe turns one row of the settings page into a source to test, saved or
// not. The saved token and pin come along only while the address is the one
// saved under that ID, so a token never goes to an address it was not
// saved for.
func Probe(e Edit, saved []Source) (Source, error) {
	addr, ok := normalize(e.Address)
	if !ok {
		return Source{}, &AddressError{Code: "invalid_address"}
	}
	src := Source{Address: addr}
	for _, prev := range saved {
		if prev.ID == e.ID && e.ID != "" && prev.Address == addr {
			src = prev
		}
	}
	if t := strings.TrimSpace(e.Token); t != "" {
		src.Token = t
	}
	if src.Kind() == KindURL && src.Token == "" {
		return Source{}, &AddressError{Code: "token_missing"}
	}
	return src, nil
}

func (s *Store) save(list []Source) error {
	if list == nil {
		list = []Source{}
	}
	b, err := json.MarshalIndent(file{Sources: list}, "", "  ")
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
	return os.Rename(tmp, s.path)
}

func public(list []Source) []Public {
	out := make([]Public, 0, len(list))
	for _, src := range list {
		out = append(out, Public{ID: src.ID, Address: src.Address, Kind: src.Kind(), HasToken: src.Token != "", Pinned: src.Pinned()})
	}
	return out
}

// normalize returns the address in the one form it is stored in: a clean
// absolute path to a .sock file, or an http(s) URL without credentials,
// query or trailing slash.
func normalize(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "/") {
		if path.Clean(raw) != raw || !strings.HasSuffix(raw, ".sock") || strings.ContainsAny(raw, "\x00\n") {
			return "", false
		}
		return raw, true
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", false
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), true
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var cfgLine = regexp.MustCompile(`^\s*([A-Z_]+)\s*=\s*"?(.*?)"?\s*$`)

// fromWidget reads BV_URL and WIDGET_TOKEN from the old widget's cfg. The
// token field there also took the whole widget link, ?token=… included.
func fromWidget(legacy string) (Source, bool) {
	if legacy == "" {
		return Source{}, false
	}
	f, err := os.Open(legacy)
	if err != nil {
		return Source{}, false
	}
	defer func() { _ = f.Close() }()
	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if m := cfgLine.FindStringSubmatch(sc.Text()); m != nil {
			vals[m[1]] = m[2]
		}
	}
	addr, ok := normalize(vals["BV_URL"])
	token := strings.TrimSpace(vals["WIDGET_TOKEN"])
	if strings.Contains(token, "://") {
		if u, err := url.Parse(token); err == nil {
			token = u.Query().Get("token")
		}
	}
	if !ok || token == "" || strings.HasPrefix(addr, "/") {
		return Source{}, false
	}
	return Source{ID: newID(), Address: addr, Token: token}, true
}
