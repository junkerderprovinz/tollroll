package hub

import (
	"context"
	"errors"
	"testing"
	"time"

	kit "github.com/junkerderprovinz/parleyport/kit/fortress"
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
