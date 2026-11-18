package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/junkerderprovinz/parleyport/kit/notify"
)

type runner struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *runner) Run(_ context.Context, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{name}, args...))
	return nil
}

func open(t *testing.T, saved string) (*Store, string, *runner) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notify.json")
	if saved != "" {
		if err := os.WriteFile(path, []byte(saved), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := &runner{}
	s, err := Open(path, notify.Options{Unraid: r})
	if err != nil {
		t.Fatal(err)
	}
	return s, path, r
}

func matrix(homeserver, token string) notify.Channel {
	return notify.Channel{ID: "matrix", Kind: notify.KindMatrix, Enabled: true, On: notify.OnAlways,
		Matrix: &notify.Matrix{Homeserver: homeserver, Room: "!room:example.org", Token: token}}
}

func saved(t *testing.T, path string) []notify.Channel {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []notify.Channel
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestWithoutAFileNothingIsSent(t *testing.T) {
	s, _, r := open(t, "")
	if s.Enabled() || len(s.Public()) != 0 {
		t.Fatalf("enabled %v, channels %+v", s.Enabled(), s.Public())
	}
	s.Send(context.Background(), notify.Event{Severity: notify.SeverityFailure, Title: "x"})
	if len(r.calls) != 0 {
		t.Errorf("sent %v", r.calls)
	}
}

func TestAFileThatDoesNotParseStopsTheStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notify.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, notify.Options{}); err == nil {
		t.Fatal("Open accepts a broken file")
	}
}

func TestTheBrowserNeverSeesASecret(t *testing.T) {
	s, _, _ := open(t, `[{"id":"matrix","kind":"matrix","enabled":true,"matrix":{"homeserver":"https://m.example.org","room":"!r:example.org","token":"syt_secret"}}]`)
	b, _ := json.Marshal(s.Public())
	if strings.Contains(string(b), "syt_secret") {
		t.Fatalf("public channels carry the token: %s", b)
	}
}

func TestASavedSecretStaysWhenTheBrowserSendsItBackMasked(t *testing.T) {
	s, path, _ := open(t, "")
	if _, err := s.Replace([]notify.Channel{matrix("https://m.example.org", "syt_secret")}); err != nil {
		t.Fatal(err)
	}
	pub := s.Public()
	pub[0].Matrix.Room = "!other:example.org"
	if _, err := s.Replace(pub); err != nil {
		t.Fatal(err)
	}
	got := saved(t, path)[0].Matrix
	if got.Token != "syt_secret" || got.Room != "!other:example.org" {
		t.Errorf("saved %+v", got)
	}
}

func TestASecretNeverFollowsAChangedAddress(t *testing.T) {
	s, path, _ := open(t, "")
	if _, err := s.Replace([]notify.Channel{matrix("https://m.example.org", "syt_secret")}); err != nil {
		t.Fatal(err)
	}
	pub := s.Public()
	pub[0].Matrix.Homeserver = "https://evil.example.net"
	if _, err := s.Replace(pub); err == nil {
		t.Fatal("a matrix channel without its token was saved")
	}
	if got := saved(t, path)[0].Matrix; got.Homeserver != "https://m.example.org" || got.Token != "syt_secret" {
		t.Errorf("a refused list changed the file: %+v", got)
	}
}

func TestChannelsAreSavedForRootOnly(t *testing.T) {
	s, path, _ := open(t, "")
	if _, err := s.Replace([]notify.Channel{{ID: "unraid", Kind: notify.KindUnraid, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %o, want 600", fi.Mode().Perm())
	}
}

func TestSendReachesTheEnabledChannels(t *testing.T) {
	s, _, r := open(t, `[{"id":"unraid","kind":"unraid","enabled":true,"on":"always"},{"id":"off","kind":"unraid","enabled":false}]`)
	if !s.Enabled() {
		t.Fatal("not enabled")
	}
	s.Send(context.Background(), notify.Event{Severity: notify.SeverityWarning, Title: "Alpha is not reachable", Body: "b"})
	if len(r.calls) != 1 || !strings.Contains(strings.Join(r.calls[0], " "), "-e TollRoll -s Alpha is not reachable") {
		t.Errorf("calls %q", r.calls)
	}
}

func TestATestTakesTheSavedSecretOnlyToTheSavedAddress(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Header.Get("Authorization"))
		mu.Unlock()
	}))
	defer srv.Close()
	hook := func(url string) notify.Channel {
		return notify.Channel{ID: "hook", Kind: notify.KindWebhook, Enabled: true,
			Webhook: &notify.Webhook{URL: url, Method: http.MethodPost, Headers: map[string]string{"Authorization": "Bearer hook-secret"}}}
	}
	s, _, _ := open(t, "")
	if _, err := s.Replace([]notify.Channel{hook(srv.URL + "/a")}); err != nil {
		t.Fatal(err)
	}
	e := notify.Event{Title: "test"}
	masked := s.Public()[0]
	if r := s.Test(context.Background(), masked, e); !r.OK() {
		t.Fatalf("test of the saved channel: %v", r.Err)
	}
	moved := s.Public()[0]
	moved.Webhook.URL = srv.URL + "/b"
	s.Test(context.Background(), moved, e)
	if len(got) != 2 || got[0] != "Bearer hook-secret" || got[1] != "" {
		t.Errorf("authorization headers %q, want the secret only at the saved address", got)
	}
}

func TestARelativeLinkStaysForUnraidAndLeadsToTheServerElsewhere(t *testing.T) {
	var got string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Link string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		got = b.Link
	}))
	defer hook.Close()
	s, _, r := open(t, "")
	s.Base = func() string { return "http://tower.local:81" }
	if _, err := s.Replace([]notify.Channel{
		{ID: "unraid", Kind: notify.KindUnraid, Enabled: true, On: notify.OnAlways},
		{ID: "hook", Kind: notify.KindWebhook, Enabled: true, On: notify.OnAlways, Webhook: &notify.Webhook{URL: hook.URL, Method: "POST", Body: `{"link":"%%link%%"}`}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, res := range s.Send(context.Background(), notify.Event{Title: "x", Link: "/Settings/FireSquire"}) {
		if !res.OK() {
			t.Fatalf("%+v", res)
		}
	}
	if got != "http://tower.local:81/Settings/FireSquire" {
		t.Errorf("the webhook got %q", got)
	}
	if len(r.calls) != 1 || !strings.Contains(strings.Join(r.calls[0], " "), "-l /Settings/FireSquire") {
		t.Errorf("unraid %v", r.calls)
	}
}

func TestWithoutTheServersAddressARelativeLinkGoesOnlyToUnraid(t *testing.T) {
	var got = "unset"
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Link string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		got = b.Link
	}))
	defer hook.Close()
	s, _, _ := open(t, "")
	s.Base = func() string { return "" }
	if _, err := s.Replace([]notify.Channel{
		{ID: "hook", Kind: notify.KindWebhook, Enabled: true, On: notify.OnAlways, Webhook: &notify.Webhook{URL: hook.URL, Method: "POST", Body: `{"link":"%%link%%"}`}},
	}); err != nil {
		t.Fatal(err)
	}
	s.Send(context.Background(), notify.Event{Title: "x", Link: "/Settings/FireSquire"})
	if got != "" {
		t.Errorf("the webhook got %q", got)
	}
}

func TestTheServersAddressComesFromNginxIni(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nginx.ini")
	if err := os.WriteFile(p, []byte("NGINX_LANIP=\"192.168.1.10\"\nNGINX_DEFAULTURL=\"https://tower.local:4443\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DefaultURL(p); got != "https://tower.local:4443" {
		t.Errorf("%q", got)
	}
	if got := DefaultURL(filepath.Join(t.TempDir(), "none")); got != "" {
		t.Errorf("missing file %q", got)
	}
}
