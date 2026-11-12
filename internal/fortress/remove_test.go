package fortress

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	kit "github.com/junkerderprovinz/parleyport/kit/fortress"
	"github.com/junkerderprovinz/parleyport/kit/group"
	"github.com/junkerderprovinz/parleyport/kit/member"
)

// memberID waits until the map shows app and returns its member ID.
func memberID(t *testing.T, s *Service, app string) string {
	t.Helper()
	var id string
	waitFor(t, app+" on the map", func() bool {
		for _, mb := range s.Map().Members {
			if mb.App == app {
				id = mb.ID
				return true
			}
		}
		return false
	})
	return id
}

var bravoRemoved = []Removed{{App: "bravo", Name: "Bravo", Device: "attic"}}

func TestARemovedMemberLeavesTheMapAndStaysNamed(t *testing.T) {
	url := relayServer(t)
	secret, words := newPhrase(t)
	openApp(t, url, secret, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	openApp(t, url, secret, "bravo", "Bravo", "attic", app{id: "bravo", name: "Bravo"})
	dir := filepath.Join(t.TempDir(), "fortress")
	s := openService(t, dir, url)
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	memberID(t, s, "alpha")

	if err := s.Remove(memberID(t, s, "bravo")); err != nil {
		t.Fatal(err)
	}
	m := s.Map()
	if hasApp(m, "bravo") || !hasApp(m, "alpha") {
		t.Errorf("after the removal the map shows %+v", m.Members)
	}
	if !slices.Equal(m.Removed, bravoRemoved) {
		t.Errorf("removed = %+v, want %+v", m.Removed, bravoRemoved)
	}
	for _, src := range s.Sources() {
		if src.Name == "Bravo" {
			t.Error("the tile still reads the removed member")
		}
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, removedFile))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("the removed list is readable by others: %v", info.Mode())
		}
	}
}

func TestTheTileCannotRemoveItselfOrAStranger(t *testing.T) {
	url := relayServer(t)
	secret, words := newPhrase(t)
	alpha := openApp(t, url, secret, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	s := openService(t, filepath.Join(t.TempDir(), "fortress"), url)
	if err := s.Remove("anyone"); !errors.Is(err, ErrNotJoined) {
		t.Errorf("a removal outside a fortress: %v, want ErrNotJoined", err)
	}
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alpha to know the tile", func() bool { return len(tilesOf(alpha)) == 1 })
	for _, id := range []string{tilesOf(alpha)[0].ID, "nobody"} {
		if err := s.Remove(id); !errors.Is(err, ErrNoMember) {
			t.Errorf("Remove(%s) = %v, want ErrNoMember", id, err)
		}
	}
	if m := s.Map(); !m.Joined || len(m.Removed) != 0 {
		t.Errorf("after the refusals the map is %+v", m)
	}
}

// A member that was away during its removal comes back with the keys it
// held. The tile restarted in between and still turns it away.
func TestARemovedMemberStaysOutAfterARestart(t *testing.T) {
	url := relayServer(t)
	secret, words := newPhrase(t)
	in := newInstance(t)
	bravo := in.open(t, url, secret, nil, "bravo", "Bravo", "attic", app{id: "bravo", name: "Bravo"})
	dir := filepath.Join(t.TempDir(), "fortress")
	s := openService(t, dir, url)
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	id := memberID(t, s, "bravo")
	waitFor(t, "bravo to know the tile", func() bool { return len(tilesOf(bravo)) == 1 })
	tile := tilesOf(bravo)[0].ID
	if err := bravo.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "bravo to go", func() bool {
		for _, mb := range s.Map().Members {
			if mb.App == "bravo" {
				return !mb.Online
			}
		}
		return false
	})
	if err := s.Remove(id); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	again := openService(t, dir, url)
	if got := again.Map().Removed; !slices.Equal(got, bravoRemoved) {
		t.Fatalf("after a restart removed = %+v", got)
	}
	back := in.open(t, url, secret, nil, "bravo", "Bravo", "attic", app{id: "bravo", name: "Bravo"})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _ = back.Status(ctx, tile, "")
		cancel()
		if hasApp(again.Map(), "bravo") {
			t.Fatal("the removed member is on the map again after a restart")
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, src := range again.Sources() {
		if src.Name == "Bravo" {
			t.Fatal("the tile reads the removed member after a restart")
		}
	}
}

func TestTheSamePhraseKeepsWhoWasRemovedAndAnotherForgetsIt(t *testing.T) {
	url := relayServer(t)
	first, firstWords := newPhrase(t)
	_, secondWords := newPhrase(t)
	openApp(t, url, first, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	openApp(t, url, first, "bravo", "Bravo", "attic", app{id: "bravo", name: "Bravo"})
	s := openService(t, filepath.Join(t.TempDir(), "fortress"), url)
	if err := s.Join(firstWords); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(memberID(t, s, "bravo")); err != nil {
		t.Fatal(err)
	}

	if err := s.Join(firstWords); err != nil {
		t.Fatal(err)
	}
	if got := s.Map().Removed; !slices.Equal(got, bravoRemoved) {
		t.Errorf("joining again with the same phrase: removed = %+v", got)
	}
	if err := s.Join(secondWords); err != nil {
		t.Fatal(err)
	}
	if got := s.Map().Removed; len(got) != 0 {
		t.Errorf("under another phrase removed = %+v", got)
	}
}

func TestFollowingANewPhraseForgetsWhoWasRemoved(t *testing.T) {
	url := relayServer(t)
	s := openService(t, filepath.Join(t.TempDir(), "fortress"), url)
	changedPhrase(t, url, s, func(old []byte) {
		openApp(t, url, old, "bravo", "Bravo", "attic", app{id: "bravo", name: "Bravo"})
		if err := s.Remove(memberID(t, s, "bravo")); err != nil {
			t.Fatal(err)
		}
		if got := s.Map().Removed; !slices.Equal(got, bravoRemoved) {
			t.Fatalf("removed = %+v", got)
		}
	})
	alpha := s.Sources()[0]
	if _, err := s.Reader().Report(context.Background(), alpha, ""); err != nil {
		t.Fatal(err)
	}
	if m := s.Map(); m.PhraseChanged || len(m.Removed) != 0 {
		t.Errorf("after following the new phrase the map is %+v", m)
	}
}

func TestTheMapSaysWhereAMembersWebPageIs(t *testing.T) {
	got, ok := entry(kit.Member{ID: "m", Kind: member.KindInstance, App: kit.App{ID: "bombvault", Name: "BombVault"},
		State: group.StateApproved, Address: "http://cellar:3000"})
	if !ok || got.Address != "http://cellar:3000" {
		t.Errorf("%+v, %v", got, ok)
	}
}
