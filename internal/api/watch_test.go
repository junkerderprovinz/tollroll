package api

import (
	"testing"
)

func TestAFindingOfTheWatchIsMarkedAndTheBoardReadAgain(t *testing.T) {
	e := newEnv(t)
	e.member("m1", app("alpha", "Alpha", "1.0.0"))
	e.do("GET", "/api/board", "")
	read := len(e.f.langs)
	code, out := e.do("POST", "/api/watch", `{"id":"unraid:7","as":"incident","note":"not me"}`)
	if code != 200 || out["ok"] != true {
		t.Fatalf("%d %v", code, out)
	}
	if got := e.marks; len(got) != 1 || got[0] != [3]string{"unraid:7", "incident", "not me"} {
		t.Errorf("%v", got)
	}
	e.do("GET", "/api/board", "")
	if len(e.f.langs) == read {
		t.Error("the board kept the view from before the mark")
	}
	if code, out := e.do("POST", "/api/watch", `{"id":"gone","as":"known"}`); code != 404 || out["error"] != "no_entry" {
		t.Errorf("%d %v", code, out)
	}
}
