package fortress

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	kit "github.com/junkerderprovinz/parleyport/kit/fortress"
	"github.com/junkerderprovinz/parleyport/kit/group"
	"github.com/junkerderprovinz/parleyport/kit/member"
	"github.com/junkerderprovinz/parleyport/kit/phrase"
	"github.com/junkerderprovinz/parleyport/kit/relay"
	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/board"
)

func relayServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(relay.New())
	t.Cleanup(srv.CloseClientConnections)
	t.Cleanup(srv.Close)
	return srv.URL
}

func newPhrase(t *testing.T) (secret []byte, words string) {
	t.Helper()
	secret, words, err := phrase.New()
	if err != nil {
		t.Fatal(err)
	}
	return secret, words
}

type memStore struct {
	mu  sync.Mutex
	raw []byte
}

func (m *memStore) Load() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return bytes.Clone(m.raw), nil
}

func (m *memStore) Save(raw []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.raw = bytes.Clone(raw)
	return nil
}

// app reports itself in the status contract and keeps a log of two lines.
type app struct {
	id, name string
}

func (a app) Report(_ context.Context, lang string) (status.Report, error) {
	name := a.name
	if lang == "de" {
		name += " (de)"
	}
	return status.Report{
		App:   status.App{ID: a.id, Name: name, Version: "1.2.3"},
		State: status.StateOK,
	}, nil
}

func (a app) Log(_ context.Context, q status.LogQuery) (status.LogPage, error) {
	all := []status.LogEntry{
		{ID: "1", At: time.Unix(1700000000, 0).UTC(), Level: status.SeverityInfo, Text: a.name + " started"},
		{ID: "2", At: time.Unix(1700000060, 0).UTC(), Level: status.SeverityWarn, Text: a.name + " is low on space"},
	}
	page := status.LogPage{Next: q.After}
	for _, e := range all {
		if e.ID > q.After {
			page.Entries = append(page.Entries, e)
			page.Next = e.ID
		}
	}
	return page, nil
}

// openApp starts an instance of an app on device in the fortress of secret.
// A nil src makes a member that serves no status, as a plugin joined on the
// command line does.
func openApp(t *testing.T, url string, secret []byte, id, name, device string, src status.Source) *kit.Fortress {
	t.Helper()
	ident, err := member.NewIdentity(member.KindInstance)
	if err != nil {
		t.Fatal(err)
	}
	f, err := kit.Open(kit.Config{
		App:      kit.App{ID: id, Name: name, Version: "1.2.3"},
		Secret:   secret,
		Identity: ident,
		Self:     group.Self{Name: device, Deployment: "container"},
		Relay:    group.RelayOwn,
		RelayURL: url,
		Store:    &memStore{},
		Status:   src,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func openService(t *testing.T, dir, url string) *Service {
	t.Helper()
	s, err := Open(Options{Dir: dir, Device: "tower", Version: "0.2.0", Relay: url})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func appsOf(srcs []board.Source) []string {
	var out []string
	for _, src := range srcs {
		out = append(out, src.Name)
	}
	return out
}

func TestThePhraseShowsEveryAppOfTheFortress(t *testing.T) {
	url := relayServer(t)
	secret, words := newPhrase(t)
	openApp(t, url, secret, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	openApp(t, url, secret, "bravo", "Bravo", "attic", app{id: "bravo", name: "Bravo"})

	s := openService(t, filepath.Join(t.TempDir(), "fortress"), url)
	if s.Map().Joined {
		t.Fatal("joined before the phrase was given")
	}
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both apps", func() bool { return len(s.Sources()) == 2 })

	srcs := s.Sources()
	if got := strings.Join(appsOf(srcs), ","); got != "Alpha,Bravo" {
		t.Errorf("sources are named %q, want Alpha,Bravo", got)
	}
	r := s.Reader()
	for _, src := range srcs {
		rep, err := r.Report(context.Background(), src, "de")
		if err != nil {
			t.Fatalf("report of %s: %v", src.Name, err)
		}
		if rep.App.Name != src.Name+" (de)" || rep.App.Version != "1.2.3" {
			t.Errorf("report of %s is %+v", src.Name, rep.App)
		}
	}
}

func TestTheLogIsReadOnFromTheCursor(t *testing.T) {
	url := relayServer(t)
	secret, words := newPhrase(t)
	openApp(t, url, secret, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	s := openService(t, filepath.Join(t.TempDir(), "fortress"), url)
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alpha", func() bool { return len(s.Sources()) == 1 })
	src := s.Sources()[0]
	r := s.Reader()

	page, err := r.Log(context.Background(), src, "", "", status.MaxLogLimit)
	if err != nil || len(page.Entries) != 2 || page.Next != "2" {
		t.Fatalf("first page %+v, %v", page, err)
	}
	page, err = r.Log(context.Background(), src, "1", "", status.MaxLogLimit)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Text != "Alpha is low on space" {
		t.Errorf("page after 1 is %+v, %v", page, err)
	}
}

func TestAMemberWithoutAStatusLeavesTheTile(t *testing.T) {
	url := relayServer(t)
	secret, words := newPhrase(t)
	openApp(t, url, secret, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	openApp(t, url, secret, "squire", "Squire", "tower", nil)
	s := openService(t, filepath.Join(t.TempDir(), "fortress"), url)
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both apps", func() bool { return len(s.Sources()) == 2 })

	r := s.Reader()
	for _, src := range s.Sources() {
		if _, err := r.Report(context.Background(), src, ""); src.Name == "Squire" && err == nil {
			t.Error("a member without a status answers")
		}
	}
	if got := strings.Join(appsOf(s.Sources()), ","); got != "Alpha" {
		t.Errorf("after reading, the tile reads %q, want only Alpha", got)
	}
	if !hasApp(s.Map(), "squire") {
		t.Error("the map lost the member without a status")
	}
}

func TestAnotherTileIsOnTheMapButNoRowOfItsOwn(t *testing.T) {
	url := relayServer(t)
	secret, words := newPhrase(t)
	openApp(t, url, secret, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	other := openService(t, filepath.Join(t.TempDir(), "other"), url)
	if err := other.Join(words); err != nil {
		t.Fatal(err)
	}
	s := openService(t, filepath.Join(t.TempDir(), "fortress"), url)
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alpha and the other tile on the map", func() bool { return hasApp(s.Map(), "alpha") && hasApp(s.Map(), AppID) })
	if got := strings.Join(appsOf(s.Sources()), ","); got != "Alpha" {
		t.Errorf("the tile reads %q, want only Alpha", got)
	}
}

func TestTheMapShowsDevicesAppsAndHowEachIsReached(t *testing.T) {
	url := relayServer(t)
	secret, words := newPhrase(t)
	openApp(t, url, secret, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	b := openApp(t, url, secret, "bravo", "Bravo", "attic", app{id: "bravo", name: "Bravo"})
	s := openService(t, filepath.Join(t.TempDir(), "fortress"), url)
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both apps on the map", func() bool {
		m := s.Map()
		return len(m.Members) == 2 && m.Members[0].App != "" && m.Members[1].App != "" && m.Members[0].Online && m.Members[1].Online
	})

	m := s.Map()
	if !m.Joined || m.Device != "tower" {
		t.Errorf("map of %q, joined %v", m.Device, m.Joined)
	}
	want := map[string]string{"alpha": "cellar", "bravo": "attic"}
	for _, mb := range m.Members {
		if want[mb.App] != mb.Device || mb.Name == "" || mb.Version != "1.2.3" || mb.Kind != "instance" {
			t.Errorf("member %+v", mb)
		}
	}
	if m.Members[0].Device > m.Members[1].Device {
		t.Errorf("members are not in device order: %s, %s", m.Members[0].Device, m.Members[1].Device)
	}

	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "bravo to go away", func() bool {
		for _, mb := range s.Map().Members {
			if mb.App == "bravo" {
				return !mb.Online
			}
		}
		return false
	})
}

func hasApp(m Map, id string) bool {
	for _, mb := range m.Members {
		if mb.App == id {
			return true
		}
	}
	return false
}

func TestAWrongPhraseIsRefusedAndNothingKept(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fortress")
	s := openService(t, dir, "")
	_, words := newPhrase(t)
	w := strings.Fields(words)
	w[0], w[1] = w[1], w[0]
	for _, bad := range []string{"", "one two three", strings.Join(w, " ")} {
		if err := s.Join(bad); !errors.Is(err, ErrPhrase) {
			t.Errorf("Join(%q) = %v, want ErrPhrase", bad, err)
		}
	}
	if s.Map().Joined {
		t.Error("a wrong phrase joined")
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a wrong phrase left %s behind: %v", dir, err)
	}
}

func TestTheMembershipOutlivesARestartAndKeepsNoPhrase(t *testing.T) {
	url := relayServer(t)
	secret, words := newPhrase(t)
	openApp(t, url, secret, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	dir := filepath.Join(t.TempDir(), "fortress")
	s := openService(t, dir, url)
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alpha", func() bool { return len(s.Sources()) == 1 })
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	again := openService(t, dir, url)
	if m := again.Map(); !m.Joined || m.Device != "tower" {
		t.Fatalf("after a restart the map is %+v", m)
	}
	waitFor(t, "alpha after the restart", func() bool { return len(again.Sources()) == 1 })

	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	for _, f := range files {
		raw, err := os.ReadFile(f) //nolint:gosec // G304: a file of the test
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, secret) || bytes.Contains(raw, []byte(hex.EncodeToString(secret))) || bytes.Contains(raw, []byte(strings.Fields(words)[0]+" ")) {
			t.Errorf("%s keeps the phrase", f)
		}
	}
}

func TestAnotherPhraseMovesToAnotherFortress(t *testing.T) {
	url := relayServer(t)
	first, firstWords := newPhrase(t)
	second, secondWords := newPhrase(t)
	openApp(t, url, first, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	openApp(t, url, second, "bravo", "Bravo", "attic", app{id: "bravo", name: "Bravo"})
	s := openService(t, filepath.Join(t.TempDir(), "fortress"), url)
	if err := s.Join(firstWords); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alpha", func() bool { return len(s.Sources()) == 1 })

	if err := s.Join(secondWords); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "bravo alone", func() bool {
		return strings.Join(appsOf(s.Sources()), ",") == "Bravo"
	})
	if hasApp(s.Map(), "alpha") {
		t.Error("the map still shows the old fortress")
	}
}

func TestLeavingForgetsTheFortress(t *testing.T) {
	url := relayServer(t)
	secret, words := newPhrase(t)
	openApp(t, url, secret, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	dir := filepath.Join(t.TempDir(), "fortress")
	s := openService(t, dir, url)
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alpha", func() bool { return len(s.Sources()) == 1 })

	if err := s.Leave(); err != nil {
		t.Fatal(err)
	}
	if m := s.Map(); m.Joined || len(m.Members) > 0 || len(s.Sources()) > 0 {
		t.Errorf("after leaving: %+v, %d sources", m, len(s.Sources()))
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("leaving kept %s: %v", dir, err)
	}
	if again := openService(t, dir, url); again.Map().Joined {
		t.Error("a restart joined again after leaving")
	}
}

func TestRemovedByAnotherMemberTheTileLeaves(t *testing.T) {
	url := relayServer(t)
	secret, words := newPhrase(t)
	a := openApp(t, url, secret, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	dir := filepath.Join(t.TempDir(), "fortress")
	s := openService(t, dir, url)
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	var self string
	waitFor(t, "alpha to see the tile", func() bool {
		for _, m := range a.Members() {
			if m.App.ID == AppID {
				self = m.ID
				return true
			}
		}
		return false
	})
	if err := a.Remove(self); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the tile to leave", func() bool { m := s.Map(); return !m.Joined && m.Removed })
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a removed tile kept %s: %v", dir, err)
	}
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	if m := s.Map(); !m.Joined || m.Removed {
		t.Errorf("joining again after removal: %+v", m)
	}
}
