package hub

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	kit "github.com/junkerderprovinz/parleyport/kit/fortress"
	"github.com/junkerderprovinz/parleyport/kit/notify"
)

func run(t *testing.T, e *env, body string) error {
	t.Helper()
	act := e.h.Action(func(id string) string { return map[string]string{"bombvault": "BombVault"}[id] })
	if act.Name != DeliverAction {
		t.Fatalf("name %q", act.Name)
	}
	_, err := act.Run(context.Background(), kit.Caller{ID: "m1", App: "bombvault"}, []byte(body))
	return err
}

// offered counts the messages the hub has taken a decision on.
func (e *env) offered() int {
	e.h.mu.Lock()
	defer e.h.mu.Unlock()
	return len(e.h.day)
}

func code(err error) string {
	var no *kit.Refusal
	if errors.As(err, &no) {
		return no.Code
	}
	return ""
}

func TestAnAppsNotificationIsTakenOnlyWhileTheHubTakesOver(t *testing.T) {
	e := newEnv(t, Settings{})
	ev := `{"kind":"backup.failed","severity":"failure","title":"Backup <b>failed</b>","body":"Job \"photos\" stopped","link":"javascript:alert(1)"}`
	if err := run(t, e, ev); code(err) != "hub_off" {
		t.Fatalf("taken while off: %v", err)
	}
	if err := e.st.Set(Settings{Quiet: Window{From: "22:00", To: "07:00"}, Digest: Daily{At: "08:00"}, TakeOver: true}); err != nil {
		t.Fatal(err)
	}
	e.ch.on = false
	if err := run(t, e, ev); code(err) != "hub_off" {
		t.Fatalf("taken without a channel: %v", err)
	}
	e.ch.on = true
	if err := run(t, e, ev); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(2 * time.Second); len(e.ch.titles()) == 0 && time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
	}
	e.ch.mu.Lock()
	defer e.ch.mu.Unlock()
	if len(e.ch.sent) != 1 {
		t.Fatalf("%+v", e.ch.sent)
	}
	got := e.ch.sent[0]
	if got.Title != "BombVault: Backup bfailed/b" || got.Body != "Job 'photos' stopped" || got.Link != "" || got.Fields["app"] != "BombVault" {
		t.Errorf("%+v", got)
	}
}

func TestADeliveryOutOfShapeIsRefused(t *testing.T) {
	e := newEnv(t, Settings{TakeOver: true})
	for _, body := range []string{
		`not json`,
		`{"kind":"Backup Failed","title":"x"}`,
		`{"kind":"backup.failed","title":"  "}`,
		`{"kind":"backup.failed","title":"x","severity":"loud"}`,
		`{"resolved":true}`,
	} {
		if err := run(t, e, body); code(err) != "bad_event" {
			t.Errorf("%s: %v", body, err)
		}
	}
	if err := run(t, e, `{"key":"job-7","resolved":true}`); err != nil {
		t.Errorf("a resolve: %v", err)
	}
}

func TestTheLinkOfADeliveryReachesTheChannelsByEitherName(t *testing.T) {
	for _, field := range []string{"link", "url"} {
		e := newEnv(t, Settings{TakeOver: true})
		body := `{"kind":"health.warn","severity":"warning","title":"Disk 2 runs hot","` + field + `":"http://tower/Settings/FireSquire"}`
		if err := run(t, e, body); err != nil {
			t.Fatal(err)
		}
		for end := time.Now().Add(2 * time.Second); len(e.ch.titles()) == 0 && time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		}
		e.ch.mu.Lock()
		if len(e.ch.sent) != 1 || e.ch.sent[0].Link != "http://tower/Settings/FireSquire" {
			t.Errorf("%s: %+v", field, e.ch.sent)
		}
		e.ch.mu.Unlock()
	}
}

func TestADeliveryTheHubTakesIsLoggedForItsApp(t *testing.T) {
	e := newEnv(t, Settings{TakeOver: true})
	j := &journal{}
	e.h.o.Journal = j
	if err := run(t, e, `{"kind":"health.warn","severity":"warning","title":"x"}`); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(2 * time.Second); j.count() == 0 && time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
	}
	if got := j.names(); len(got) != 1 || got[0] != "BombVault" {
		t.Errorf("%q", got)
	}
}

type journal struct {
	mu        sync.Mutex
	delivered []string
}

func (j *journal) Told(notify.Event, []notify.Result) {}

func (j *journal) Delivered(name string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.delivered = append(j.delivered, name)
}

func (j *journal) count() int { return len(j.names()) }

func (j *journal) names() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.delivered...)
}

func TestADeliveryKeepsAPathOnTheServerAndDropsAnyOtherLink(t *testing.T) {
	for link, want := range map[string]string{
		"/Settings/FireSquire":      "/Settings/FireSquire",
		"//evil.example/x":          "",
		"javascript:alert(1)":       "",
		"https://tower.local/x?y=1": "https://tower.local/x?y=1",
	} {
		e := newEnv(t, Settings{TakeOver: true})
		if err := run(t, e, `{"kind":"health.warn","title":"x","link":"`+link+`"}`); err != nil {
			t.Fatal(err)
		}
		for end := time.Now().Add(2 * time.Second); len(e.ch.titles()) == 0 && time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		}
		e.ch.mu.Lock()
		if len(e.ch.sent) != 1 || e.ch.sent[0].Link != want {
			t.Errorf("%s: %+v", link, e.ch.sent)
		}
		e.ch.mu.Unlock()
	}
}

// dispatched sends through the kit's own dispatcher, which decides which
// channel takes an event.
type dispatched struct{ d *notify.Dispatcher }

func (d dispatched) Enabled() bool { return true }

func (d dispatched) Send(ctx context.Context, e notify.Event) []notify.Result {
	return d.d.Send(ctx, e)
}

func TestAnAllClearReachesAChannelForProblemsThroughTheHub(t *testing.T) {
	hits := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hits <- string(b)
	}))
	defer srv.Close()
	d, err := notify.NewDispatcher([]notify.Channel{{ID: "problems", Kind: notify.KindWebhook, Enabled: true, On: notify.OnFailure,
		Webhook: &notify.Webhook{URL: srv.URL, Body: "%%title%%"}}}, notify.Options{})
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, Settings{TakeOver: true})
	e.h = New(Options{Channels: dispatched{d}, Settings: e.st, State: filepath.Join(t.TempDir(), "state.json"),
		Texts: func() (string, map[string]string) { return "en", texts },
		Now:   func() time.Time { return e.now }})

	if err := run(t, e, `{"kind":"check.go","severity":"success","title":"Back to normal","key":"verdict"}`); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-hits:
		t.Fatalf("a plain success reached the channel for problems: %q", got)
	case <-time.After(300 * time.Millisecond):
	}
	if err := run(t, e, `{"kind":"check.go","severity":"success","title":"All clear","key":"verdict-2","recovery":true}`); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-hits:
		if got != "BombVault: All clear" {
			t.Errorf("the channel got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the all-clear never reached the channel for problems")
	}
}

func TestAnAllClearPassesAnAppsLevelAndAWaitAsTheProblemDid(t *testing.T) {
	e := newEnv(t, Settings{TakeOver: true, Apps: map[string]string{"bombvault": LevelFailure}})
	ok := `{"kind":"check.go","severity":"success","title":"Fine again"}`
	back := `{"kind":"check.go","severity":"success","title":"All clear","recovery":true}`
	for _, body := range []string{ok, back} {
		if err := run(t, e, body); err != nil {
			t.Fatal(err)
		}
	}
	// Both deliveries run on their own, so the clock moves only once both read it.
	for end := time.Now().Add(2 * time.Second); (len(e.ch.titles()) == 0 || e.offered() < 2) && time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
	}
	e.ch.mu.Lock()
	if len(e.ch.sent) != 1 || e.ch.sent[0].Title != "BombVault: All clear" || !e.ch.sent[0].Recovery {
		t.Errorf("an app on failures got %+v", e.ch.sent)
	}
	e.ch.sent = nil
	e.ch.mu.Unlock()

	e.at("23:00")
	e.h.Offer(context.Background(), Message{App: "bombvault", Name: "BombVault", Event: notify.Event{Kind: "check.go", Severity: notify.SeveritySuccess, Title: "Clear at night", Recovery: true}})
	e.at("07:30")
	e.h.Tick(context.Background())
	e.ch.mu.Lock()
	defer e.ch.mu.Unlock()
	if len(e.ch.sent) != 1 || !e.ch.sent[0].Recovery {
		t.Errorf("what waited through the night: %+v", e.ch.sent)
	}
}
