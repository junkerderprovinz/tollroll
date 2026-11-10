package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/junkerderprovinz/parleyport/kit/notify"
	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/alert"
	"github.com/junkerderprovinz/tollroll/internal/board"
	"github.com/junkerderprovinz/tollroll/internal/channels"
	"github.com/junkerderprovinz/tollroll/internal/fetch"
	"github.com/junkerderprovinz/tollroll/internal/sources"
)

type fake struct {
	mu     sync.Mutex
	apps   map[string]status.Report
	tokens map[string]string
	langs  []string
}

func (f *fake) Report(_ context.Context, src sources.Source, lang string) (status.Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.langs = append(f.langs, lang)
	if f.tokens == nil {
		f.tokens = map[string]string{}
	}
	f.tokens[src.Address] = src.Token
	rep, ok := f.apps[src.Address]
	if !ok {
		return status.Report{}, &fetch.Error{Code: "unreachable"}
	}
	return rep, nil
}

func (f *fake) Log(context.Context, sources.Source, string, string, int) (status.LogPage, error) {
	return status.LogPage{}, nil
}

type env struct {
	t      *testing.T
	f      *fake
	store  *sources.Store
	h      http.Handler
	dir    string
	runner *runner
	fort   *fakeFortress
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	store, err := sources.Open(filepath.Join(dir, "sources.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	f := &fake{apps: map[string]status.Report{}}
	socks := filepath.Join(dir, "run")
	_ = os.Mkdir(socks, 0o700)
	b := board.New(store.List, f)
	r := &runner{}
	ch, err := channels.Open(filepath.Join(dir, "notify.json"), notify.Options{Unraid: r})
	if err != nil {
		t.Fatal(err)
	}
	texts := func() (string, alert.Texts) {
		return "en", alert.Texts{"n_test_title": "TollRoll test", "n_test_body": "It works."}
	}
	fort := &fakeFortress{}
	return &env{t: t, f: f, store: store, dir: socks, runner: r, fort: fort, h: New(store, b, f, socks, ch, texts, fort)}
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
	e.f.apps["https://a.lan"] = app("alpha", "Alpha", "1.0.0")
	_, _ = e.store.Replace([]sources.Edit{{Address: "https://a.lan", Token: "secret"}})
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
	e.f.apps["/run/a.sock"] = app("alpha", "Alpha", "1")
	_, _ = e.store.Replace([]sources.Edit{{Address: "/run/a.sock"}})
	e.do("GET", "/api/board?lang=../../etc", "")
	if e.f.langs[0] != "" {
		t.Fatalf("lang %q reached the app", e.f.langs[0])
	}
}

func TestTheSourceListCarriesNoToken(t *testing.T) {
	e := newEnv(t)
	e.f.apps["https://a.lan"] = app("alpha", "Alpha", "1.0.0")
	_, _ = e.store.Replace([]sources.Edit{{Address: "https://a.lan", Token: "secret-token"}})
	e.do("GET", "/api/board", "")
	req := httptest.NewRequest("GET", "/api/sources", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "secret-token") {
		t.Fatalf("token in %s", rec.Body)
	}
	var out struct {
		Sources []struct {
			Address  string `json:"address"`
			HasToken bool   `json:"hasToken"`
			Last     *board.Outcome
		} `json:"sources"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Sources) != 1 || !out.Sources[0].HasToken || out.Sources[0].Last == nil || out.Sources[0].Last.Name != "Alpha" {
		t.Fatalf("sources %s", rec.Body)
	}
}

func TestSourcesAreSaved(t *testing.T) {
	e := newEnv(t)
	code, out := e.do("PUT", "/api/sources", `{"sources":[{"address":"https://a.lan","token":"t"}]}`)
	if code != 200 || len(out["sources"].([]any)) != 1 {
		t.Fatalf("code %d, %v", code, out)
	}
	if got := e.store.List(); len(got) != 1 || got[0].Token != "t" {
		t.Fatalf("store %+v", got)
	}
}

func TestARefusedListNamesTheRow(t *testing.T) {
	e := newEnv(t)
	code, out := e.do("PUT", "/api/sources", `{"sources":[{"address":"/run/a.sock"},{"address":"nope"}]}`)
	if code != 400 || out["error"] != "invalid_address" || out["index"] != 1.0 {
		t.Fatalf("code %d, %v", code, out)
	}
}

func TestATestUsesTheSavedToken(t *testing.T) {
	e := newEnv(t)
	e.f.apps["https://a.lan"] = app("alpha", "Alpha", "1.0.0")
	pub, _ := e.store.Replace([]sources.Edit{{Address: "https://a.lan", Token: "saved"}})
	code, out := e.do("POST", "/api/test", `{"id":"`+pub[0].ID+`","address":"https://a.lan"}`)
	if code != 200 || out["ok"] != true || out["name"] != "Alpha" || out["version"] != "1.0.0" {
		t.Fatalf("code %d, %v", code, out)
	}
	if e.f.tokens["https://a.lan"] != "saved" {
		t.Fatalf("token %q", e.f.tokens["https://a.lan"])
	}
}

func TestATestOfANewAddressTakesTheTypedToken(t *testing.T) {
	e := newEnv(t)
	e.f.apps["https://b.lan"] = app("bravo", "Bravo", "2")
	pub, _ := e.store.Replace([]sources.Edit{{Address: "https://a.lan", Token: "saved"}})
	_, out := e.do("POST", "/api/test", `{"id":"`+pub[0].ID+`","address":"https://b.lan","token":"typed"}`)
	if out["ok"] != true || e.f.tokens["https://b.lan"] != "typed" {
		t.Fatalf("%v, token %q", out, e.f.tokens["https://b.lan"])
	}
}

func TestASavedTokenNeverGoesToANewAddress(t *testing.T) {
	e := newEnv(t)
	e.f.apps["https://evil.lan"] = app("x", "X", "1")
	pub, _ := e.store.Replace([]sources.Edit{{Address: "https://a.lan", Token: "saved"}})
	e.do("POST", "/api/test", `{"id":"`+pub[0].ID+`","address":"https://evil.lan"}`)
	if got := e.f.tokens["https://evil.lan"]; got != "" {
		t.Fatalf("saved token %q went to another address", got)
	}
}

func TestAFailedTestSaysWhy(t *testing.T) {
	e := newEnv(t)
	code, out := e.do("POST", "/api/test", `{"address":"https://gone.lan","token":"t"}`)
	if code != 200 || out["ok"] != false || out["error"] != "unreachable" {
		t.Fatalf("code %d, %v", code, out)
	}
	_, out = e.do("POST", "/api/test", `{"address":"ftp://x"}`)
	if out["error"] != "invalid_address" {
		t.Fatalf("%v", out)
	}
}

func TestSearchingFindsAppsOnSockets(t *testing.T) {
	e := newEnv(t)
	for _, n := range []string{"alpha.sock", "quiet.sock", "known.sock", "notes.txt"} {
		_ = os.WriteFile(filepath.Join(e.dir, n), nil, 0o600)
	}
	alpha := filepath.ToSlash(filepath.Join(e.dir, "alpha.sock"))
	known := filepath.ToSlash(filepath.Join(e.dir, "known.sock"))
	e.f.apps[alpha] = app("alpha", "Alpha", "1.0.0")
	e.f.apps[known] = app("kilo", "Kilo", "1")
	if _, err := e.store.Replace([]sources.Edit{{Address: known}}); err != nil {
		t.Skipf("socket paths need a unix root: %v", err)
	}
	code, out := e.do("GET", "/api/discover", "")
	found := out["found"].([]any)
	if code != 200 || len(found) != 1 {
		t.Fatalf("code %d, %v", code, out)
	}
	f := found[0].(map[string]any)
	if f["address"] != alpha || f["name"] != "Alpha" {
		t.Fatalf("found %v", f)
	}
}

func TestOnlyTheKnownMethodsAnswer(t *testing.T) {
	e := newEnv(t)
	for _, c := range [][2]string{{"POST", "/api/board"}, {"DELETE", "/api/sources"}, {"GET", "/api/test"}} {
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
	code, _ := e.do("PUT", "/api/sources", `{"sources":[{"address":"`+strings.Repeat("a", 2<<20)+`"}]}`)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code %d", code)
	}
}
