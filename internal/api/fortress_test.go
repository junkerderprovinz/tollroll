package api

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/junkerderprovinz/tollroll/internal/fortress"
)

type fakeFortress struct {
	mu      sync.Mutex
	joined  string
	fail    error
	removed []fortress.Removed
}

func (f *fakeFortress) Map() fortress.Map {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := fortress.Map{Members: []fortress.Member{}}
	if f.joined != "" {
		m.Joined, m.Device = true, "tower"
		m.Removed = f.removed
		if len(f.removed) == 0 {
			m.Members = append(m.Members, fortress.Member{ID: "m1", App: "alpha", Name: "Alpha", Device: "cellar", Online: true})
		}
	}
	return m
}

func (f *fakeFortress) Remove(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.fail != nil:
		return f.fail
	case f.joined == "":
		return fortress.ErrNotJoined
	case id != "m1" || len(f.removed) > 0:
		return fortress.ErrNoMember
	}
	f.removed = []fortress.Removed{{App: "alpha", Name: "Alpha", Device: "cellar"}}
	return nil
}

func (f *fakeFortress) Join(words string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	if len(strings.Fields(words)) != 12 {
		return fortress.ErrPhrase
	}
	f.joined = words
	return nil
}

func (f *fakeFortress) Leave() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.joined = ""
	return nil
}

const twelve = "one two three four five six seven eight nine ten eleven twelve"

func TestTheMapOfTheFortressIsServed(t *testing.T) {
	e := newEnv(t)
	code, out := e.do("GET", "/api/fortress", "")
	if code != 200 || out["joined"] != false || len(out["members"].([]any)) != 0 {
		t.Fatalf("%d %v", code, out)
	}
	e.fort.joined = twelve
	_, out = e.do("GET", "/api/fortress", "")
	members := out["members"].([]any)
	if out["joined"] != true || out["device"] != "tower" || len(members) != 1 || members[0].(map[string]any)["device"] != "cellar" {
		t.Fatalf("map %v", out)
	}
}

func TestJoiningTakesThePhraseAndGivesItNeverBack(t *testing.T) {
	e := newEnv(t)
	code, out := e.do("POST", "/api/fortress/join", `{"phrase":"`+twelve+`"}`)
	if code != 200 || out["joined"] != true {
		t.Fatalf("%d %v", code, out)
	}
	if e.fort.joined != twelve {
		t.Errorf("the service joined with %q", e.fort.joined)
	}
	for _, path := range []string{"/api/fortress", "/api/board"} {
		req := e.raw("GET", path, "")
		if strings.Contains(req, "eleven") {
			t.Errorf("%s gives the phrase back: %s", path, req)
		}
	}
}

func TestAWrongPhraseIsSaidSo(t *testing.T) {
	e := newEnv(t)
	if code, out := e.do("POST", "/api/fortress/join", `{"phrase":"one two"}`); code != 400 || out["error"] != "phrase_invalid" {
		t.Errorf("%d %v", code, out)
	}
	if code, out := e.do("POST", "/api/fortress/join", `not json`); code != 400 || out["error"] != "bad_request" {
		t.Errorf("%d %v", code, out)
	}
	e.fort.fail = errors.New("disk full")
	if code, out := e.do("POST", "/api/fortress/join", `{"phrase":"`+twelve+`"}`); code != 500 || out["error"] != "join_failed" {
		t.Errorf("%d %v", code, out)
	}
}

func TestLeavingIsServed(t *testing.T) {
	e := newEnv(t)
	e.fort.joined = twelve
	code, out := e.do("POST", "/api/fortress/leave", "")
	if code != 200 || out["joined"] != false || e.fort.joined != "" {
		t.Fatalf("%d %v", code, out)
	}
}

func TestRemovingAMemberAnswersWithTheMapThatNamesIt(t *testing.T) {
	e := newEnv(t)
	e.fort.joined = twelve
	code, out := e.do("POST", "/api/fortress/remove", `{"id":"m1"}`)
	if code != 200 || len(out["members"].([]any)) != 0 {
		t.Fatalf("%d %v", code, out)
	}
	removed := out["removed"].([]any)
	if len(removed) != 1 || removed[0].(map[string]any)["name"] != "Alpha" || removed[0].(map[string]any)["device"] != "cellar" {
		t.Errorf("removed %v", out["removed"])
	}
}

func TestARemovalThatCannotBeIsSaidSo(t *testing.T) {
	e := newEnv(t)
	if code, out := e.do("POST", "/api/fortress/remove", `{"id":"m1"}`); code != 409 || out["error"] != "not_joined" {
		t.Errorf("outside a fortress: %d %v", code, out)
	}
	e.fort.joined = twelve
	if code, out := e.do("POST", "/api/fortress/remove", `{"id":"m9"}`); code != 404 || out["error"] != "no_member" {
		t.Errorf("a stranger: %d %v", code, out)
	}
	if code, out := e.do("POST", "/api/fortress/remove", `[`); code != 400 || out["error"] != "bad_request" {
		t.Errorf("no body: %d %v", code, out)
	}
	e.fort.fail = errors.New("disk full")
	if code, out := e.do("POST", "/api/fortress/remove", `{"id":"m1"}`); code != 500 || out["error"] != "remove_failed" {
		t.Errorf("a failed removal: %d %v", code, out)
	}
}
