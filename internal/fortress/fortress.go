// Package fortress keeps TollRoll's place in a fortress. Joined with the
// phrase, the tile reads the status and log of every member with its own
// member key. The phrase itself is never kept.
//
// The tile takes part as an instance of the app "tollroll" that offers no
// action and uses none: it reads the others and reports only its own status.
// On this network it takes their calls on a port of its own, elsewhere the
// relay carries them.
package fortress

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	"github.com/junkerderprovinz/tollroll/internal/journal"
)

// AppID is the app the tile joins as.
const AppID = "tollroll"

//go:embed logo.svg
var logoSVG []byte

// Icon is the TollRoll mark the other members show for the tile.
var Icon, _ = status.IconFromSVG(logoSVG)

// silentFor is how long a member that serves no status stays off the tile
// before it is asked again, in case its app learned to report.
const silentFor = 10 * time.Minute

// followKit takes the membership to the keys of a changed phrase, and
// followFor is how long a member that answered on the old phrase gets to hand
// them over.
var (
	followKit = kit.Follow
	followFor = 10 * time.Second
)

// ErrPhrase is a phrase that is not twelve words of the list.
var ErrPhrase = errors.New("fortress: the phrase is not valid")

// ErrNoMember is a removal of an ID that is no other member of the fortress.
var ErrNoMember = errors.New("fortress: no other member has that id")

// ErrShutOut is a join the members turned away, since one of them was taken
// out and the phrase has not changed since.
var ErrShutOut = errors.New("fortress: no new member until the phrase changes")

// admitWait is how long a join waits for a member to answer the tile or turn
// it away.
var admitWait = 8 * time.Second

// removedFile keeps the members the tile took out, with the keys of the
// phrase they were taken out under.
const removedFile = "removed.json"

// removedHereFile is all that stays of a membership another member ended, so
// the tile still says so after a restart.
const removedHereFile = "removed-here"

// Options say where the membership is kept and how the members are reached.
type Options struct {
	Dir string
	// Device is the name of this server, which the other members show and
	// find the apps on this server by.
	Device  string
	Version string
	// Relay is "" for the project's relay, "off" or the address of an own one.
	Relay string
	// LAN finds the members on this network too.
	LAN bool
	// Journal is what the other members read about the tile, nil for no
	// status.
	Journal *journal.Journal
}

// Map is the fortress as the tile sees it: this server and every other
// member, each connected or not. How the kit carries a call is its own
// business and stays off the map.
type Map struct {
	Joined bool `json:"joined"`
	// RemovedHere says another member took the tile out, until it joins
	// again.
	RemovedHere bool `json:"removedHere,omitempty"`
	// Removed are the members the tile took out since the phrase last
	// changed, which keeps the fortress closed to new members until it
	// changes.
	Removed []Removed `json:"removed,omitempty"`
	// PhraseChanged says the fortress has a new phrase the tile could not
	// follow on its own, until the new words are entered.
	PhraseChanged bool     `json:"phraseChanged,omitempty"`
	Device        string   `json:"device,omitempty"`
	Members       []Member `json:"members"`
}

// Member is one instance in the fortress. App is its app's ID, the key of
// its row on the board. Icon is its app's logo as a checked SVG data URI.
// Relay says only the relay reaches it. OnDemand marks an app that is in
// reach only while it runs, so being away is how it should be.
type Member struct {
	ID         string    `json:"id"`
	App        string    `json:"app"`
	Name       string    `json:"name"`
	Version    string    `json:"version"`
	Icon       string    `json:"icon,omitempty"`
	Device     string    `json:"device"`
	Deployment string    `json:"deployment"`
	Kind       string    `json:"kind"`
	Online     bool      `json:"online"`
	Relay      bool      `json:"relay,omitempty"`
	OnDemand   bool      `json:"onDemand,omitempty"`
	LastSeen   time.Time `json:"lastSeen,omitzero"`
}

// Removed is a member the tile took out of the fortress.
type Removed struct {
	App    string `json:"app"`
	Name   string `json:"name"`
	Device string `json:"device"`
}

type removedList struct {
	Group   string    `json:"group"`
	Members []Removed `json:"members"`
}

// names are how the house's apps write their names, for a member that
// announces its app by the ID alone or without a name.
var names = map[string]string{
	"arrowloop": "ArrowLoop", "bombvault": "BombVault", "cannoncommander": "CannonCommander",
	"castleparcel": "CastleParcel", "firesquire": "FireSquire", "knightloader": "KnightLoader",
	"shiplog": "ShipLog", "tollroll": "TollRoll", "towertorch": "TowerTorch",
}

// onDemand says the app took part through the kit's fortress command, which
// announces it by its ID alone and stays only while one command runs, as a
// plugin's check does.
func onDemand(a kit.App) bool { return a.Name == a.ID }

func appName(a kit.App) string {
	if a.Name != "" && a.Name != a.ID {
		return a.Name
	}
	if n, ok := names[a.ID]; ok {
		return n
	}
	return a.ID
}

// entry is mb as the map shows it, false while it has not said which app it
// runs, since there is no name to show yet.
func entry(mb kit.Member) (Member, bool) {
	if mb.State != group.StateApproved || mb.App.ID == "" {
		return Member{}, false
	}
	e := Member{
		ID: mb.ID, App: mb.App.ID, Name: appName(mb.App), Version: mb.App.Version,
		Device: mb.Device, Deployment: mb.Deployment, Kind: string(mb.Kind),
		Online: mb.Online(), Relay: mb.Relay && !mb.Direct, OnDemand: mb.Kind == member.KindInstance && onDemand(mb.App), LastSeen: mb.LastSeen,
	}
	if status.CheckIcon(mb.App.Icon) == nil {
		e.Icon = mb.App.Icon
	}
	return e, true
}

// Service is the tile's membership. It is safe for concurrent use.
type Service struct {
	opt Options

	mu            sync.Mutex
	f             *kit.Fortress
	lan           *lan.Service
	direct        *http.Server
	device        string
	removedHere   bool
	phraseChanged bool
	// group names the keys the fortress runs on and out the members taken out
	// under them.
	group  string
	out    []Removed
	silent map[string]time.Time
	// following lets one follow run at a time without holding mu, so the
	// tile is not kept waiting.
	following sync.Mutex
	// gen counts the fortresses opened, so a removal that arrives late does
	// not end a membership joined after it.
	gen int
}

// Open starts the membership kept in o.Dir, if the tile joined before.
func Open(o Options) (*Service, error) {
	s := &Service{opt: o, silent: map[string]time.Time{}}
	cfg, err := kit.Load(o.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		_, err := os.Stat(filepath.Join(o.Dir, removedHereFile))
		s.removedHere = err == nil
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

// Join takes the tile into the fortress of words. A tile in another fortress
// leaves it first. It keeps its member key, so after a phrase change it
// could not follow, the others know it as before. It fails with ErrShutOut
// and forgets the membership when the members turn the tile away.
func (s *Service) Join(words string) error {
	s.mu.Lock()
	removed := s.removedHere
	s.mu.Unlock()
	if err := s.join(words); err != nil {
		return err
	}
	s.mu.Lock()
	f := s.f
	s.mu.Unlock()
	if f == nil || !turnedAway(f, admitWait) {
		return nil
	}
	return s.shutOut(f, removed)
}

// turnedAway waits up to wait for every member in reach to answer f, and
// reports whether the members turned it away instead. A member taken out
// still answers, so one answer alone says nothing.
func turnedAway(f *kit.Fortress, wait time.Duration) bool {
	for end := time.Now().Add(wait); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if f.ShutOut() {
			return true
		}
		if allAnswered(f) {
			return false
		}
	}
	return f.ShutOut()
}

// allAnswered reports whether f knows a member in reach and every one of them
// told it which app it runs, which a member that turns f away never does.
func allAnswered(f *kit.Fortress) bool {
	n := 0
	for _, mb := range f.Members() {
		if mb.Kind != member.KindInstance || !mb.Online() {
			continue
		}
		if mb.App.ID == "" {
			return false
		}
		n++
	}
	return n > 0
}

// shutOut forgets the membership of f, which nobody lets in until the phrase
// changes, so the card offers the join again. A tile that another member
// took out still says so.
func (s *Service) shutOut(f *kit.Fortress, removed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f != f {
		return ErrShutOut
	}
	err := errors.Join(s.stop(), os.RemoveAll(s.opt.Dir))
	if removed {
		s.removedHere = true
		err = errors.Join(err, os.MkdirAll(s.opt.Dir, 0o700))
		err = errors.Join(err, os.WriteFile(filepath.Join(s.opt.Dir, removedHereFile), nil, 0o600))
	}
	return errors.Join(ErrShutOut, err)
}

func (s *Service) join(words string) error {
	secret, err := phrase.Decode(words)
	if err != nil {
		return ErrPhrase
	}
	defer clear(secret)

	next := s.opt.Dir + ".new"
	if err := os.RemoveAll(next); err != nil {
		return err
	}
	if err := keepKey(s.opt.Dir, next); err != nil {
		return err
	}
	if err := kit.Join(next, secret, kit.Membership{App: AppID, Device: s.opt.Device, Relay: s.opt.Relay, LAN: s.opt.LAN}); err != nil {
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
	s.removedHere, s.phraseChanged = false, false
	return s.start(cfg)
}

// keepKey copies the membership kept in dir to next, where kit.Join finds
// the member key and joins under it. The members taken out come along and
// stay named if the phrase is the same.
func keepKey(dir, next string) error {
	for _, name := range []string{"fortress.json", removedFile} {
		raw, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: the operator names the directory
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := os.MkdirAll(next, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(next, name), raw, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// Leave takes the tile out of its fortress and forgets the membership. The
// members in reach drop it at once, the others when they hear it from them.
func (s *Service) Leave() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removedHere, s.phraseChanged = false, false
	if s.f != nil {
		s.f.Leave()
	}
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
	cfg.App = kit.App{ID: AppID, Name: "TollRoll", Version: s.opt.Version, Icon: Icon}
	cfg.Host = s.opt.Device
	if j := s.opt.Journal; j != nil {
		cfg.Status = j
		j.SetInstance(status.Instance{ID: cfg.Identity.ID(), Name: cfg.Self.Name})
	}
	var ln net.Listener
	if cfg.LAN != nil {
		var err error
		if ln, err = listenDirect(); err != nil {
			return err
		}
		if ln != nil {
			cfg.Self.DirectURL = "http://" + ln.Addr().String()
		}
		cfg.LAN.Start()
	}
	s.gen++
	gen := s.gen
	cfg.OnRemoved = func() { s.removedBy(gen) }
	f, err := kit.Open(cfg)
	if err != nil {
		if ln != nil {
			_ = ln.Close()
		}
		if cfg.LAN != nil {
			_ = cfg.LAN.Close()
		}
		return err
	}
	if ln != nil {
		s.direct = group.ServeDirect(ln, f.DirectHandler())
	}
	s.f, s.lan, s.device = f, cfg.LAN, cfg.Self.Name
	s.group, s.out = cfg.Keys.Group, loadRemoved(s.opt.Dir, cfg.Keys.Group)
	clear(s.silent)
	return nil
}

// listenDirect opens the port the other members call the tile on, on the
// address it announces on this network and no other. The OS picks the port,
// since the announce carries it. A host without an IPv4 address gets none and
// reaches the members through the relay alone.
func listenDirect() (net.Listener, error) {
	ip := lan.LocalIPv4()
	if ip == "" {
		return nil, nil
	}
	return net.Listen("tcp4", net.JoinHostPort(ip, "0"))
}

func (s *Service) stop() error {
	var errs []error
	if s.direct != nil {
		errs = append(errs, s.direct.Close())
	}
	if s.f != nil {
		errs = append(errs, s.f.Close())
	}
	if s.lan != nil {
		errs = append(errs, s.lan.Close())
	}
	s.f, s.lan, s.direct, s.device = nil, nil, nil, ""
	s.group, s.out = "", nil
	return errors.Join(errs...)
}

// Remove takes member id out of the fortress, here and at every member in
// reach, and names it until the phrase changes. The kit keeps it out after a
// restart as well. The tile's own ID is no other member: it leaves
// instead.
func (s *Service) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return ErrNotJoined
	}
	for _, mb := range s.f.Members() {
		if mb.ID != id {
			continue
		}
		if err := s.f.Remove(id); err != nil {
			return err
		}
		out := append(slices.Clone(s.out), Removed{App: mb.App.ID, Name: appName(mb.App), Device: mb.Device})
		if err := saveRemoved(s.opt.Dir, removedList{Group: s.group, Members: out}); err != nil {
			return err
		}
		s.out = out
		if s.opt.Journal != nil {
			s.opt.Journal.Removed(appName(mb.App), mb.Device)
		}
		return nil
	}
	return ErrNoMember
}

// loadRemoved reads the members taken out under the keys of group. A list
// kept under other keys is from a phrase the fortress has left, which lets
// nobody back in, so it goes.
func loadRemoved(dir, group string) []Removed {
	path := filepath.Join(dir, removedFile)
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the operator names the directory
	if err != nil {
		return nil
	}
	var l removedList
	if json.Unmarshal(raw, &l) != nil || l.Group != group {
		_ = os.Remove(path)
		return nil
	}
	return l.Members
}

// saveRemoved replaces the list in dir, readable by its owner alone, so that
// a crash leaves the old list or the new one.
func saveRemoved(dir string, l removedList) error {
	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".removed-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, removedFile))
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
		if os.MkdirAll(s.opt.Dir, 0o700) == nil {
			_ = os.WriteFile(filepath.Join(s.opt.Dir, removedHereFile), nil, 0o600)
		}
		s.removedHere, s.phraseChanged = true, false
	}()
}

// Map returns the fortress as the tile sees it now.
func (s *Service) Map() Map {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := Map{RemovedHere: s.removedHere, PhraseChanged: s.phraseChanged, Members: []Member{}}
	if s.f == nil {
		return m
	}
	m.Joined, m.Device, m.Removed = true, s.device, slices.Clone(s.out)
	for _, mb := range s.f.Members() {
		if e, ok := entry(mb); ok {
			m.Members = append(m.Members, e)
		}
	}
	slices.SortFunc(m.Members, func(a, b Member) int {
		return cmp.Or(cmp.Compare(a.Device, b.Device), cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return m
}

// Sources returns the members the tile reads: every instance of another app
// that said which app it runs, less those that serve no status. An app in
// reach only while it runs serves none and would read as down all day.
func (s *Service) Sources() []board.Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	now := time.Now()
	var out []board.Source
	for _, mb := range s.f.Members() {
		if mb.State != group.StateApproved || mb.Kind != member.KindInstance || mb.App.ID == "" || mb.App.ID == AppID || onDemand(mb.App) {
			continue
		}
		if at, ok := s.silent[mb.ID]; ok && now.Sub(at) < silentFor {
			continue
		}
		out = append(out, board.Source{ID: mb.ID, Name: appName(mb.App)})
	}
	slices.SortFunc(out, func(a, b board.Source) int { return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID)) })
	return out
}

// Source is member id as the board reads it, for its status window. Any
// instance that said which app it runs can be asked, the ones off the tile
// as well.
func (s *Service) Source(id string) (board.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return board.Source{}, ErrNotJoined
	}
	for _, mb := range s.f.Members() {
		if mb.ID == id && mb.State == group.StateApproved && mb.Kind == member.KindInstance && mb.App.ID != "" {
			return board.Source{ID: mb.ID, Name: appName(mb.App)}, nil
		}
	}
	return board.Source{}, ErrNoMember
}

// ErrNotJoined is a read while the tile is in no fortress.
var ErrNotJoined = errors.New("fortress: not joined")

// Reader reads the members through the tile's membership.
func (s *Service) Reader() board.Reader { return reader{s: s} }

type reader struct{ s *Service }

func (r reader) Report(ctx context.Context, src board.Source, lang string) (status.Report, error) {
	f := r.s.current()
	if f == nil {
		return status.Report{}, ErrNotJoined
	}
	rep, err := f.Status(ctx, src.ID, lang)
	if moved := r.s.followed(ctx, f, src.ID, err); moved != nil {
		rep, err = moved.Status(ctx, src.ID, lang)
	}
	return rep, r.s.failed(src.ID, err)
}

func (r reader) Log(ctx context.Context, src board.Source, after, lang string, limit int) (status.LogPage, error) {
	f := r.s.current()
	if f == nil {
		return status.LogPage{}, ErrNotJoined
	}
	q := status.LogQuery{After: after, Lang: lang, Limit: limit}
	page, err := f.Log(ctx, src.ID, q)
	if moved := r.s.followed(ctx, f, src.ID, err); moved != nil {
		page, err = moved.Log(ctx, src.ID, q)
	}
	return page, r.s.failed(src.ID, err)
}

// followed is the fortress under the new keys when err says member id
// answered on the phrase the fortress left and the tile could follow, else
// nil. The follow takes its own time, since a read's deadline is too short
// for the kit to ask the member what it is and fetch the keys.
func (s *Service) followed(ctx context.Context, f *kit.Fortress, id string, err error) *kit.Fortress {
	var no *kit.Refusal
	if !errors.As(err, &no) || no.Code != "phrase_changed" {
		return nil
	}
	moved := s.follow(context.WithoutCancel(ctx), f)
	if moved != nil {
		reach(ctx, moved, id)
	}
	return moved
}

// follow takes the membership in Dir to the fortress's new keys and opens it
// again, returning the fortress under them, or nil where none came. f is the
// fortress that ran into the old phrase.
func (s *Service) follow(ctx context.Context, f *kit.Fortress) *kit.Fortress {
	s.following.Lock()
	defer s.following.Unlock()
	if cur := s.current(); cur != f {
		return cur
	}
	deadline := time.Now().Add(followFor)
	moved, err := followKit(ctx, s.opt.Dir, f)
	for !moved && err == nil && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		moved, err = followKit(ctx, s.opt.Dir, f)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f != f {
		return s.f
	}
	if moved {
		_ = s.stop()
		cfg, err := kit.Load(s.opt.Dir)
		if err == nil && s.start(cfg) == nil {
			s.phraseChanged = false
			return s.f
		}
	}
	s.phraseChanged = true
	return nil
}

// reach waits until member id is online in f, which has just opened, or ctx
// ends.
func reach(ctx context.Context, f *kit.Fortress, id string) {
	for {
		for _, m := range f.Members() {
			if m.ID == id && m.Online() {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (s *Service) current() *kit.Fortress {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f
}

// failed leaves a member that serves no status, such as a plugin joined on
// the command line, off the tile for a while.
func (s *Service) failed(id string, err error) error {
	var no *kit.Refusal
	if errors.As(err, &no) && no.Code == "not_found" {
		s.mu.Lock()
		s.silent[id] = time.Now()
		s.mu.Unlock()
	}
	return err
}
