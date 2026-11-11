package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/junkerderprovinz/parleyport/kit/notify"
	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/alert"
	"github.com/junkerderprovinz/tollroll/internal/board"
	"github.com/junkerderprovinz/tollroll/internal/channels"
	"github.com/junkerderprovinz/tollroll/internal/prefs"
)

// fake answers for the members of the fortress by their ID.
type fake struct {
	mu    sync.Mutex
	apps  map[string]status.Report
	langs []string
}

func (f *fake) Report(_ context.Context, src board.Source, lang string) (status.Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.langs = append(f.langs, lang)
	rep, ok := f.apps[src.ID]
	if !ok {
		return status.Report{}, errors.New("unreachable")
	}
	return rep, nil
}

func (f *fake) Log(context.Context, board.Source, string, string, int) (status.LogPage, error) {
	return status.LogPage{}, nil
}

type env struct {
	t      *testing.T
	f      *fake
	h      http.Handler
	runner *runner
	fort   *fakeFortress

	mu   sync.Mutex
	srcs []board.Source
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{t: t, f: &fake{apps: map[string]status.Report{}}, runner: &runner{}, fort: &fakeFortress{}}
	b := board.New(e.list, e.f)
	ch, err := channels.Open(filepath.Join(dir, "notify.json"), notify.Options{Unraid: e.runner})
	if err != nil {
		t.Fatal(err)
	}
	texts := func() (string, alert.Texts) {
		return "en", alert.Texts{"n_test_title": "TollRoll test", "n_test_body": "It works."}
	}
	e.h = New(b, ch, texts, e.fort, prefs.Open(filepath.Join(dir, "tile.json")))
	return e
}

func (e *env) list() []board.Source {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]board.Source(nil), e.srcs...)
}

// member puts a member that reports rep in the fortress.
func (e *env) member(id string, rep status.Report) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.srcs = append(e.srcs, board.Source{ID: id, Name: rep.App.Name})
	e.f.apps[id] = rep
}

func (e *env) do(method, target, body string) (int, map[string]any) {
	e.t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func app(id, name, version string) status.Report {
	return status.Report{Contract: 1, App: status.App{ID: id, Name: name, Version: version}, State: status.StateOK}
}

func TestTheBoardIsServed(t *testing.T) {
	e := newEnv(t)
	e.member("m1", app("alpha", "Alpha", "1.0.0"))
	code, out := e.do("GET", "/api/board?lang=de", "")
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	apps := out["apps"].([]any)
	if len(apps) != 1 || apps[0].(map[string]any)["name"] != "Alpha" {
		t.Fatalf("apps %v", apps)
	}
	if e.f.langs[0] != "de" {
		t.Fatalf("lang %q", e.f.langs[0])
	}
}

func TestAStrangeLanguageIsDropped(t *testing.T) {
	e := newEnv(t)
	e.member("m1", app("alpha", "Alpha", "1"))
	e.do("GET", "/api/board?lang=../../etc", "")
	if e.f.langs[0] != "" {
		t.Fatalf("lang %q reached the app", e.f.langs[0])
	}
}

func TestAppsAreAddedOnlyThroughTheFortress(t *testing.T) {
	e := newEnv(t)
	for _, c := range [][2]string{{"GET", "/api/sources"}, {"PUT", "/api/sources"}, {"POST", "/api/test"}, {"GET", "/api/discover"}} {
		if code, _ := e.do(c[0], c[1], `{}`); code != http.StatusNotFound {
			t.Errorf("%s %s answers %d", c[0], c[1], code)
		}
	}
}

func TestOnlyTheKnownMethodsAnswer(t *testing.T) {
	e := newEnv(t)
	for _, c := range [][2]string{{"POST", "/api/board"}, {"DELETE", "/api/tile"}, {"GET", "/api/fortress/join"}} {
		if code, _ := e.do(c[0], c[1], ""); code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: %d", c[0], c[1], code)
		}
	}
	if code, _ := e.do("GET", "/api/nothing", ""); code != http.StatusNotFound {
		t.Errorf("unknown path: %d", code)
	}
}

func TestAnOversizedBodyIsRefused(t *testing.T) {
	e := newEnv(t)
	code, _ := e.do("POST", "/api/fortress/join", `{"phrase":"`+strings.Repeat("a", 2<<20)+`"}`)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code %d", code)
	}
}
