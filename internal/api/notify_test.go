package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/junkerderprovinz/tollroll/internal/sources"
)

type runner struct {
	mu    sync.Mutex
	calls []string
}

func (r *runner) Run(_ context.Context, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil
}

func (e *env) raw(method, target, body string) string {
	e.t.Helper()
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec.Body.String()
}

func TestAnIconIsServedByTheIDItsRowCarries(t *testing.T) {
	e := newEnv(t)
	icon := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><circle r="4"/></svg>`))
	rep := app("alpha", "Alpha", "1.0.0")
	rep.App.Icon, rep.App.Color = icon, "#2e7d32"
	e.f.apps["/run/a.sock"] = rep
	_, _ = e.store.Replace([]sources.Edit{{Address: "/run/a.sock"}})
	_, board := e.do("GET", "/api/board", "")
	row := board["apps"].([]any)[0].(map[string]any)
	id, _ := row["icon"].(string)
	if id == "" || row["color"] != "#2e7d32" {
		t.Fatalf("row %v", row)
	}
	code, out := e.do("GET", "/api/icons", "")
	if code != http.StatusOK || out["icons"].(map[string]any)[id] != icon {
		t.Fatalf("code %d, icons %v", code, out)
	}
}

func TestTheChannelsNeverCarryASecret(t *testing.T) {
	e := newEnv(t)
	body := `{"channels":[{"id":"matrix","kind":"matrix","enabled":true,"matrix":{"homeserver":"https://m.example.org","room":"!r:example.org","token":"syt_secret"}}]}`
	if got := e.raw("PUT", "/api/notify", body); strings.Contains(got, "syt_secret") || !strings.Contains(got, `"homeserver":"https://m.example.org"`) {
		t.Fatalf("PUT answered %s", got)
	}
	if got := e.raw("GET", "/api/notify", ""); strings.Contains(got, "syt_secret") || !strings.Contains(got, `"id":"matrix"`) {
		t.Fatalf("GET answered %s", got)
	}
}

func TestARefusedChannelListSaysWhatIsWrong(t *testing.T) {
	e := newEnv(t)
	code, out := e.do("PUT", "/api/notify", `{"channels":[{"id":"matrix","kind":"matrix","enabled":true,"matrix":{"homeserver":"https://m.example.org","token":"t"}}]}`)
	if code != http.StatusBadRequest || out["error"] != "invalid" {
		t.Fatalf("code %d, %v", code, out)
	}
	p := out["problems"].([]any)[0].(map[string]any)
	if p["channel"] != "matrix" || p["code"] != "missing_field" {
		t.Fatalf("problem %v", p)
	}
}

func TestAChannelTestSendsTheTestMessage(t *testing.T) {
	e := newEnv(t)
	code, out := e.do("POST", "/api/notify/test", `{"channel":{"id":"unraid","kind":"unraid","enabled":false}}`)
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("code %d, %v", code, out)
	}
	if len(e.runner.calls) != 1 || !strings.Contains(e.runner.calls[0], "-s TollRoll test -d It works.") {
		t.Fatalf("calls %q", e.runner.calls)
	}
}

func TestAFailedChannelTestSaysWhy(t *testing.T) {
	e := newEnv(t)
	code, out := e.do("POST", "/api/notify/test", `{"channel":{"id":"matrix","kind":"matrix","matrix":{"homeserver":"https://m.example.org"}}}`)
	if code != http.StatusOK || out["ok"] != false || out["error"] != "invalid" {
		t.Fatalf("code %d, %v", code, out)
	}
}
