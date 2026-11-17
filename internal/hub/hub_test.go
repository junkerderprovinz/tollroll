package hub

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/notify"
	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/alert"
	"github.com/junkerderprovinz/tollroll/internal/board"
)

type channels struct {
	mu   sync.Mutex
	on   bool
	sent []notify.Event
}

func (c *channels) Enabled() bool { return c.on }

func (c *channels) Send(_ context.Context, e notify.Event) []notify.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, e)
	return []notify.Result{{Channel: "unraid", Kind: notify.KindUnraid}}
}

func (c *channels) titles() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, e := range c.sent {
		out = append(out, e.Title)
	}
	return out
}

var texts = map[string]string{
	"n_held_title":   "Collected",
	"n_held_more":    "and {n} more",
	"n_digest_title": "Your day",
	"n_digest_line":  "{name} ({n}): {title}",
	"n_digest_none":  "Nothing",
	"n_digest_open":  "Open: {n}",
}

type env struct {
	t   *testing.T
	ch  *channels
	st  *Store
	h   *Hub
	now time.Time
}

func newEnv(t *testing.T, s Settings) *env {
	dir := t.TempDir()
	e := &env{t: t, ch: &channels{on: true}, st: OpenStore(filepath.Join(dir, "hub.json")),
		now: time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)}
	if s.Apps == nil {
		s.Apps = map[string]string{}
	}
	s.Quiet.From, s.Quiet.To = cmpOr(s.Quiet.From, "22:00"), cmpOr(s.Quiet.To, "07:00")
	s.Digest.At = cmpOr(s.Digest.At, "08:00")
	if err := e.st.Set(s); err != nil {
		t.Fatal(err)
	}
	e.h = New(Options{Channels: e.ch, Settings: e.st, State: filepath.Join(dir, "state.json"),
		Texts: func() (string, map[string]string) { return "en", texts },
		Open:  func(string) []string { return []string{"BombVault: a backup is overdue"} },
		Now:   func() time.Time { return e.now }})
	return e
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (e *env) offer(app, title string, sev notify.Severity) {
	e.h.Offer(context.Background(), Message{App: app, Name: strings.ToUpper(app[:1]) + app[1:], Event: notify.Event{Kind: "x.y", Severity: sev, Title: title}})
}

func (e *env) at(hhmm string) {
	t, _ := time.Parse("15:04", hhmm)
	e.now = time.Date(e.now.Year(), e.now.Month(), e.now.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
}

func TestTheSameMessageGoesOutOnceUntilItIsResolvedOrAWhilePassed(t *testing.T) {
	e := newEnv(t, Settings{})
	e.offer("bombvault", "Backup failed", notify.SeverityFailure)
	e.offer("bombvault", "Backup failed", notify.SeverityFailure)
	if got := e.ch.titles(); len(got) != 1 {
		t.Fatalf("%q", got)
	}
	e.now = e.now.Add(21 * time.Hour)
	e.offer("bombvault", "Backup failed", notify.SeverityFailure)
	if got := e.ch.titles(); len(got) != 2 {
		t.Fatalf("after 21 hours: %q", got)
	}
	e.h.Offer(context.Background(), Message{App: "bombvault", Key: "job", Event: notify.Event{Severity: notify.SeverityFailure, Title: "Job broke"}})
	e.h.Offer(context.Background(), Message{App: "bombvault", Key: "job", Event: notify.Event{Severity: notify.SeverityFailure, Title: "Job broke again"}})
	e.h.Offer(context.Background(), Message{App: "bombvault", Key: "job", Resolved: true})
	e.h.Offer(context.Background(), Message{App: "bombvault", Key: "job", Event: notify.Event{Severity: notify.SeverityFailure, Title: "Job broke once more"}})
	if got := e.ch.titles(); strings.Join(got[2:], ",") != "Job broke,Job broke once more" {
		t.Errorf("%q", got)
	}
}

func TestASuccessEndsWhatItsKeyWasAbout(t *testing.T) {
	e := newEnv(t, Settings{})
	m := func(sev notify.Severity, title string) {
		e.h.Offer(context.Background(), Message{App: "a", Key: "reach", Event: notify.Event{Severity: sev, Title: title}})
	}
	m(notify.SeverityWarning, "down")
	m(notify.SeveritySuccess, "back")
	m(notify.SeverityWarning, "down")
	if got := strings.Join(e.ch.titles(), ","); got != "down,back,down" {
		t.Errorf("%s", got)
	}
}

func TestQuietHoursHoldAllButAlertsAndSendTheRestAsOne(t *testing.T) {
	e := newEnv(t, Settings{Quiet: Window{On: true, From: "22:00", To: "07:00"}})
	e.at("23:30")
	e.offer("shiplog", "Update ready", notify.SeverityInfo)
	e.offer("bombvault", "Backup failed", notify.SeverityFailure)
	e.offer("firesquire", "Disk overheating", notify.SeverityCritical)
	e.h.Tick(context.Background())
	if got := e.ch.titles(); len(got) != 1 || got[0] != "Disk overheating" {
		t.Fatalf("at night: %q", got)
	}
	e.now = e.now.Add(8 * time.Hour)
	e.h.Tick(context.Background())
	e.h.Tick(context.Background())
	got := e.ch.sent
	if len(got) != 2 || got[1].Title != "Collected" || got[1].Body != "Shiplog: Update ready\nBombvault: Backup failed" || got[1].Severity != notify.SeverityFailure {
		t.Errorf("in the morning: %+v", got)
	}
}

func TestAnAppSendsOnlyFromItsLevelOnButTheSummaryHasAll(t *testing.T) {
	e := newEnv(t, Settings{Apps: map[string]string{"shiplog": LevelFailure}, Digest: Daily{On: true, At: "18:00"}})
	e.offer("shiplog", "Update ready", notify.SeverityInfo)
	e.offer("shiplog", "Update check failed", notify.SeverityWarning)
	e.offer("shiplog", "Pull failed", notify.SeverityFailure)
	e.offer("shiplog", "Registry gone", notify.SeverityCritical)
	if got := strings.Join(e.ch.titles(), ","); got != "Pull failed,Registry gone" {
		t.Fatalf("%s", got)
	}
	e.at("18:01")
	e.h.Tick(context.Background())
	e.h.Tick(context.Background())
	last := e.ch.sent[len(e.ch.sent)-1]
	if len(e.ch.sent) != 3 || last.Title != "Your day" || last.Body != "Shiplog (4): Registry gone\n\nOpen: 1\nBombVault: a backup is overdue" {
		t.Errorf("%+v", last)
	}
}

func TestABurstWaitsForTheNextFreeSlot(t *testing.T) {
	e := newEnv(t, Settings{})
	for i := range 8 {
		e.offer("knightloader", "Download "+string(rune('a'+i)), notify.SeverityInfo)
	}
	e.offer("bombvault", "Backup done", notify.SeverityInfo)
	if got := e.ch.titles(); len(got) != 7 || got[6] != "Backup done" {
		t.Fatalf("%q", got)
	}
	e.h.Tick(context.Background())
	if got := e.ch.titles(); len(got) != 8 || got[7] != "Collected" {
		t.Errorf("%q", got)
	}
}

func TestTheHubKeepsWhatWaitsOverARestart(t *testing.T) {
	e := newEnv(t, Settings{Quiet: Window{On: true, From: "22:00", To: "07:00"}})
	e.at("23:00")
	e.offer("shiplog", "Update ready", notify.SeverityInfo)
	e.h.Save()
	h := New(e.h.o)
	e.now = e.now.Add(9 * time.Hour)
	h.Tick(context.Background())
	if got := e.ch.titles(); len(got) != 1 || got[0] != "Collected" {
		t.Errorf("%q", got)
	}
}

func TestSettingsOutOfShapeAreRefused(t *testing.T) {
	st := OpenStore(filepath.Join(t.TempDir(), "hub.json"))
	if got := st.Get(); got.Quiet.On || got.Digest.On || got.TakeOver || got.Quiet.From != "22:00" {
		t.Errorf("defaults %+v", got)
	}
	for _, s := range []Settings{
		{Quiet: Window{From: "25:00", To: "07:00"}, Digest: Daily{At: "08:00"}},
		{Quiet: Window{From: "22:00", To: "07:00"}, Digest: Daily{At: "8:00"}},
		{Quiet: Window{From: "22:00", To: "07:00"}, Digest: Daily{At: "08:00"}, Apps: map[string]string{"a": "loud"}},
	} {
		if err := st.Set(s); err != ErrSettings {
			t.Errorf("%+v: %v", s, err)
		}
	}
}

func TestQuietHoursMayRunPastMidnight(t *testing.T) {
	w := Window{On: true, From: "22:00", To: "07:00"}
	for hhmm, want := range map[string]bool{"21:59": false, "22:00": true, "03:00": true, "06:59": true, "07:00": false} {
		tm, _ := time.Parse("15:04", hhmm)
		if w.quiet(tm) != want {
			t.Errorf("%s", hhmm)
		}
	}
	if (Window{On: true, From: "09:00", To: "17:00"}).quiet(time.Date(1, 1, 1, 18, 0, 0, 0, time.UTC)) {
		t.Error("a window within the day")
	}
}

func TestTheServersAlertReachesUnraidWithoutAChannelOnceUntilItIsOver(t *testing.T) {
	e := newEnv(t, Settings{Quiet: Window{On: true, From: "00:00", To: "23:59"}})
	e.ch.on = false
	var unraid []string
	e.h.o.Unraid = func(_ context.Context, ev notify.Event) notify.Result {
		unraid = append(unraid, ev.Title)
		return notify.Result{Channel: "unraid", Kind: notify.KindUnraid}
	}
	full := Message{App: "unraid", Name: "Unraid", Key: "host.varlog_full", Unraid: true,
		Event: notify.Event{Kind: "host.varlog_full", Severity: notify.SeverityCritical, Title: "/var/log is almost full"}}
	e.h.Offer(context.Background(), full)
	e.h.Offer(context.Background(), full)
	if len(unraid) != 1 || len(e.ch.sent) != 0 {
		t.Fatalf("unraid %q, channels %+v", unraid, e.ch.sent)
	}
	e.h.Resolve("unraid", "host.varlog_full")
	e.ch.on = true
	e.h.Offer(context.Background(), full)
	if len(unraid) != 1 || len(e.ch.sent) != 1 {
		t.Errorf("with a channel on: unraid %q, channels %+v", unraid, e.ch.sent)
	}
	e.h.mu.Lock()
	defer e.h.mu.Unlock()
	if len(e.h.day) != 2 {
		t.Errorf("the summary keeps %d", len(e.h.day))
	}
}

func TestAnAppThatRaisesNeedsByTheDozenSendsSixAndOneCollectedMessage(t *testing.T) {
	e := newEnv(t, Settings{})
	tr := &alert.Tracker{}
	tx := alert.Texts{"n_need_title": "{name} needs you"}
	seen := func(n int) []board.Seen {
		s := board.Seen{Source: "m1", App: "knightloader", Name: "KnightLoader"}
		for i := range n {
			s.Needs = append(s.Needs, status.Need{ID: "n" + strconv.Itoa(i), Severity: status.SeverityWarn, Text: "Download " + strconv.Itoa(i) + " failed"})
		}
		return []board.Seen{s}
	}
	tr.Next(seen(0), tx)
	for _, ev := range tr.Next(seen(50), tx) {
		e.h.Send(context.Background(), ev)
	}
	if n := len(e.ch.titles()); n != 6 {
		t.Fatalf("%d sent at once, want 6", n)
	}
	e.h.Tick(context.Background())
	e.h.Tick(context.Background())
	got := e.ch.sent
	if len(got) != 7 || got[6].Title != "Collected" || !strings.HasSuffix(got[6].Body, "and 34 more") {
		t.Errorf("%d sent, last %+v", len(got), got[len(got)-1])
	}
}
