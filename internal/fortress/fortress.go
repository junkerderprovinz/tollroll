// Package fortress keeps TollRoll's place in a fortress. Joined with the
// phrase, the tile reads the status and log of every member with its own
// member key, so no app needs a read token. The phrase itself is never kept.
//
// The tile takes part as an instance of the app "tollroll" that offers no
// action and uses none: it only reads. It opens no port of its own, so it
// reaches the others directly on this network or through the relay, and
// nobody needs to reach it.
package fortress

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"os"
	"slices"
	"sync"
	"time"

	kit "github.com/junkerderprovinz/parleyport/kit/fortress"
	"github.com/junkerderprovinz/parleyport/kit/group"
	"github.com/junkerderprovinz/parleyport/kit/lan"
	"github.com/junkerderprovinz/parleyport/kit/member"
	"github.com/junkerderprovinz/parleyport/kit/phrase"
	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/board"
	"github.com/junkerderprovinz/tollroll/internal/fetch"
	"github.com/junkerderprovinz/tollroll/internal/sources"
)

// AppID is the app the tile joins as.
const AppID = "tollroll"

// silentFor is how long a member that serves no status stays off the tile
// before it is asked again, in case its app learned to report.
const silentFor = 10 * time.Minute

// ErrPhrase is a phrase that is not twelve words of the list.
var ErrPhrase = errors.New("fortress: the phrase is not valid")

// Options say where the membership is kept and how the members are reached.
type Options struct {
	Dir     string
	Version string
	// Relay is "" for the project's relay, "off" or the address of an own one.
	Relay string
	// LAN finds the members on this network too.
	LAN bool
}

// Map is the fortress as the tile sees it: this server and every other
// member, each with how the tile reaches it now.
type Map struct {
	Joined bool `json:"joined"`
	// Removed says another member took the tile out, until it joins again.
	Removed bool   `json:"removed,omitempty"`
	Device  string `json:"device,omitempty"`
	// Relay says this server is on the relay now.
	Relay   bool     `json:"relay"`
	Members []Member `json:"members"`
}

// Member is one instance in the fortress. App is its app's ID, the key of
// its row on the board, empty until it said which app it runs.
type Member struct {
	ID         string    `json:"id"`
	App        string    `json:"app"`
	Name       string    `json:"name"`
	Version    string    `json:"version"`
	Device     string    `json:"device"`
	Deployment string    `json:"deployment"`
	Kind       string    `json:"kind"`
	Direct     bool      `json:"direct"`
	Relay      bool      `json:"relay"`
	LastSeen   time.Time `json:"lastSeen,omitzero"`
}

// Service is the tile's membership. It is safe for concurrent use.
type Service struct {
	opt Options

	mu      sync.Mutex
	f       *kit.Fortress
	lan     *lan.Service
	device  string
	removed bool
	silent  map[string]time.Time
	// gen counts the fortresses opened, so a removal that arrives late does
	// not end a membership joined after it.
	gen int
}

// Open starts the membership kept in o.Dir, if the tile joined before.
func Open(o Options) (*Service, error) {
	s := &Service{opt: o, silent: map[string]time.Time{}}
	cfg, err := kit.Load(o.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.start(cfg); err != nil {
		return nil, err
	}
	return s, nil
}

// Join takes the tile into the fortress of words, as device. A tile in
// another fortress leaves it first, which is also how it follows a changed
// phrase.
func (s *Service) Join(words, device string) error {
	secret, err := phrase.Decode(words)
	if err != nil {
		return ErrPhrase
	}
	defer clear(secret)

	next := s.opt.Dir + ".new"
	if err := os.RemoveAll(next); err != nil {
		return err
	}
	if err := kit.Join(next, secret, kit.Membership{App: AppID, Device: device, Relay: s.opt.Relay, LAN: s.opt.LAN}); err != nil {
		_ = os.RemoveAll(next)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.stop(); err != nil {
		return err
	}
	if err := os.RemoveAll(s.opt.Dir); err != nil {
		return err
	}
	if err := os.Rename(next, s.opt.Dir); err != nil {
		return err
	}
	cfg, err := kit.Load(s.opt.Dir)
	if err != nil {
		return err
	}
	s.removed = false
	return s.start(cfg)
}

// Leave takes the tile out of its fortress and forgets the membership. The
// others keep it on their lists as away until somebody removes it.
func (s *Service) Leave() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removed = false
	if err := s.stop(); err != nil {
		return err
	}
	return os.RemoveAll(s.opt.Dir)
}

// Close stops taking part and keeps the membership for the next start.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stop()
}

func (s *Service) start(cfg kit.Config) error {
	cfg.App = kit.App{ID: AppID, Name: "TollRoll", Version: s.opt.Version}
	if cfg.LAN != nil {
		cfg.LAN.Start()
	}
	s.gen++
	gen := s.gen
	cfg.OnRemoved = func() { s.removedBy(gen) }
	f, err := kit.Open(cfg)
	if err != nil {
		if cfg.LAN != nil {
			_ = cfg.LAN.Close()
		}
		return err
	}
	s.f, s.lan, s.device = f, cfg.LAN, cfg.Self.Name
	clear(s.silent)
	return nil
}

func (s *Service) stop() error {
	var errs []error
	if s.f != nil {
		errs = append(errs, s.f.Close())
	}
	if s.lan != nil {
		errs = append(errs, s.lan.Close())
	}
	s.f, s.lan, s.device = nil, nil, ""
	return errors.Join(errs...)
}

// removedBy forgets the membership once another member took it out. The key
// is no good anywhere now, and joining again makes a new one. The kit calls
// it on a goroutine of the fortress, which Close waits for, so it stops the
// fortress on one of its own.
func (s *Service) removedBy(gen int) {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.gen != gen || s.f == nil {
			return
		}
		_ = s.stop()
		_ = os.RemoveAll(s.opt.Dir)
		s.removed = true
	}()
}

// Map returns the fortress as the tile sees it now.
func (s *Service) Map() Map {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := Map{Removed: s.removed, Members: []Member{}}
	if s.f == nil {
		return m
	}
	m.Joined, m.Device, m.Relay = true, s.device, s.f.RelayConnected()
	for _, mb := range s.f.Members() {
		if mb.State != group.StateApproved {
			continue
		}
		m.Members = append(m.Members, Member{
			ID: mb.ID, App: mb.App.ID, Name: mb.App.Name, Version: mb.App.Version,
			Device: mb.Device, Deployment: mb.Deployment, Kind: string(mb.Kind),
			Direct: mb.Direct, Relay: mb.Relay, LastSeen: mb.LastSeen,
		})
	}
	slices.SortFunc(m.Members, func(a, b Member) int {
		return cmp.Or(cmp.Compare(a.Device, b.Device), cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return m
}

// Sources returns the members the tile reads: every instance of another app
// that said which app it runs, less those that serve no status.
func (s *Service) Sources() []sources.Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	now := time.Now()
	var out []sources.Source
	for _, mb := range s.f.Members() {
		if mb.State != group.StateApproved || mb.Kind != member.KindInstance || mb.App.ID == "" || mb.App.ID == AppID {
			continue
		}
		if at, ok := s.silent[mb.ID]; ok && now.Sub(at) < silentFor {
			continue
		}
		out = append(out, sources.Source{ID: mb.ID, Address: sources.FortressPrefix + mb.ID, Name: mb.App.Name})
	}
	slices.SortFunc(out, func(a, b sources.Source) int { return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID)) })
	return out
}

// Reader reads the fortress members through the tile's membership and every
// other source through next.
func (s *Service) Reader(next board.Reader) board.Reader {
	return reader{s: s, next: next}
}

type reader struct {
	s    *Service
	next board.Reader
}

func (r reader) Report(ctx context.Context, src sources.Source, lang string) (status.Report, error) {
	if src.Kind() != sources.KindFortress {
		return r.next.Report(ctx, src, lang)
	}
	f := r.s.current()
	if f == nil {
		return status.Report{}, &fetch.Error{Code: "unreachable"}
	}
	rep, err := f.Status(ctx, src.ID, lang)
	return rep, r.s.failed(src.ID, err)
}

func (r reader) Log(ctx context.Context, src sources.Source, after, lang string, limit int) (status.LogPage, error) {
	if src.Kind() != sources.KindFortress {
		return r.next.Log(ctx, src, after, lang, limit)
	}
	f := r.s.current()
	if f == nil {
		return status.LogPage{}, &fetch.Error{Code: "unreachable"}
	}
	page, err := f.Log(ctx, src.ID, status.LogQuery{After: after, Lang: lang, Limit: limit})
	return page, r.s.failed(src.ID, err)
}

func (s *Service) current() *kit.Fortress {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f
}

// failed turns an error of a read into the codes the tile knows. A member
// that serves no status, such as a plugin joined on the command line, is left
// off the tile for a while.
func (s *Service) failed(id string, err error) error {
	if err == nil {
		return nil
	}
	var no *kit.Refusal
	if errors.As(err, &no) && no.Code == "not_found" {
		s.mu.Lock()
		s.silent[id] = time.Now()
		s.mu.Unlock()
		return &fetch.Error{Code: "not_found"}
	}
	return &fetch.Error{Code: "unreachable"}
}
