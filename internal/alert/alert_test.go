package alert

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/notify"
	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/board"
)

var en = Texts{
	"n_down_title": "{name} is not reachable",
	"n_down_body":  "TollRoll has not reached {name} for a few minutes.",
	"n_back_title": "{name} is reachable again",
	"n_back_body":  "TollRoll reaches {name} again.",
	"n_need_title": "{name} needs you",
}

func up(needs ...status.Need) board.Seen {
	return board.Seen{Source: "s1", Name: "Alpha", Needs: needs}
}

func down() board.Seen { return board.Seen{Source: "s1", Name: "Alpha", Down: true} }

func need(id string, sev status.Severity, text string) status.Need {
	return status.Need{ID: id, Severity: sev, Text: text}
}

func titles(es []notify.Event) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Title)
	}
	return out
}

func TestTheFirstCheckOnlyLearnsWhatIsThere(t *testing.T) {
	var tr Tracker
	if got := tr.Next([]board.Seen{up(need("n1", status.SeverityWarn, "old copy"))}, en); len(got) != 0 {
		t.Errorf("first check sent %q", titles(got))
	}
}

func TestANewNeedIsToldOnce(t *testing.T) {
	var tr Tracker
	tr.Next([]board.Seen{up()}, en)
	n := need("n1", status.SeverityWarn, "The copy is 9 days old.")
	got := tr.Next([]board.Seen{up(n)}, en)
	if len(got) != 1 {
		t.Fatalf("sent %q", titles(got))
	}
	e := got[0]
	if e.Kind != KindNeed || e.Title != "Alpha needs you" || e.Body != "The copy is 9 days old." || e.Severity != notify.SeverityWarning || e.Link != "" || e.Fields["app"] != "Alpha" {
		t.Errorf("event %+v", e)
	}
	for range 3 {
		if again := tr.Next([]board.Seen{up(n)}, en); len(again) != 0 {
			t.Fatalf("the same need was told again: %q", titles(again))
		}
	}
}

func TestANeedTakesItsSeverityAlong(t *testing.T) {
	for sev, want := range map[status.Severity]notify.Severity{
		status.SeverityInfo:  notify.SeverityInfo,
		status.SeverityWarn:  notify.SeverityWarning,
		status.SeverityError: notify.SeverityFailure,
	} {
		var tr Tracker
		tr.Next([]board.Seen{up()}, en)
		got := tr.Next([]board.Seen{up(need("n", sev, "x"))}, en)
		if len(got) != 1 || got[0].Severity != want {
			t.Errorf("%s: events %+v", sev, got)
		}
	}
}

func TestAnAppIsToldDownOnlyAfterSomeChecksAndOnce(t *testing.T) {
	var tr Tracker
	tr.Next([]board.Seen{up()}, en)
	for i := 1; i < downChecks; i++ {
		if got := tr.Next([]board.Seen{down()}, en); len(got) != 0 {
			t.Fatalf("check %d sent %q", i, titles(got))
		}
	}
	got := tr.Next([]board.Seen{down()}, en)
	if len(got) != 1 || got[0].Kind != KindDown || got[0].Title != "Alpha is not reachable" || got[0].Severity != notify.SeverityWarning {
		t.Fatalf("sent %+v", got)
	}
	for range 5 {
		if again := tr.Next([]board.Seen{down()}, en); len(again) != 0 {
			t.Fatalf("told again: %q", titles(again))
		}
	}
}

func TestAShortOutageIsNotTold(t *testing.T) {
	var tr Tracker
	tr.Next([]board.Seen{up()}, en)
	tr.Next([]board.Seen{down()}, en)
	if got := tr.Next([]board.Seen{up()}, en); len(got) != 0 {
		t.Errorf("sent %q", titles(got))
	}
	for i := 1; i < downChecks; i++ {
		if got := tr.Next([]board.Seen{down()}, en); len(got) != 0 {
			t.Fatalf("the count did not start over: %q", titles(got))
		}
	}
}

func TestAnAppDownFromTheStartIsTold(t *testing.T) {
	var tr Tracker
	var got []notify.Event
	for range downChecks {
		got = append(got, tr.Next([]board.Seen{down()}, en)...)
	}
	if len(got) != 1 || got[0].Kind != KindDown {
		t.Errorf("sent %+v", got)
	}
}

func TestTheFirstAnswerAfterADownStartOnlyLearns(t *testing.T) {
	var tr Tracker
	tr.Next([]board.Seen{down()}, en)
	if got := tr.Next([]board.Seen{up(need("n1", status.SeverityWarn, "x"))}, en); len(got) != 0 {
		t.Errorf("sent %q", titles(got))
	}
}

func TestEachSourceIsTrackedOnItsOwn(t *testing.T) {
	var tr Tracker
	b := func(needs ...status.Need) board.Seen {
		return board.Seen{Source: "s2", Name: "Beta", Needs: needs}
	}
	tr.Next([]board.Seen{up(), b()}, en)
	got := tr.Next([]board.Seen{up(), b(need("n1", status.SeverityError, "x"))}, en)
	if len(got) != 1 || got[0].Title != "Beta needs you" {
		t.Errorf("sent %q", titles(got))
	}
}

func TestARemovedSourceStartsOverWhenItReturns(t *testing.T) {
	var tr Tracker
	n := need("n1", status.SeverityWarn, "x")
	tr.Next([]board.Seen{up()}, en)
	tr.Next([]board.Seen{}, en)
	if got := tr.Next([]board.Seen{up(n)}, en); len(got) != 0 {
		t.Errorf("sent %q", titles(got))
	}
}

func TestTextsFallBackToEnglishAndFillPlaceholders(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("en.json", `{"n_down_title":"{name} is not reachable","n_back_title":"{name} is back"}`)
	write("de.json", `{"n_down_title":"{name} ist nicht erreichbar"}`)
	tx := LoadTexts(dir, "de")
	if got := tx.Get("n_down_title", "Alpha"); got != "Alpha ist nicht erreichbar" {
		t.Errorf("de: %q", got)
	}
	if got := tx.Get("n_back_title", "Alpha"); got != "Alpha is back" {
		t.Errorf("fallback: %q", got)
	}
	if got := LoadTexts(dir, "../en").Get("n_down_title", "A"); got != "A is not reachable" {
		t.Errorf("a strange language: %q", got)
	}
}

func TestTheLanguageComesFromUnraidsDisplaySettings(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "dynamix.cfg")
	for body, want := range map[string]string{
		"[display]\nlocale=\"de_DE\"\n":    "de",
		"[display]\nlocale=\"pt_BR\"\nx=1": "pt",
		"[display]\nlocale=\"\"\n":         "en",
		"[display]\n":                      "en",
	} {
		if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := Language(cfg); got != want {
			t.Errorf("%q: %q, want %q", body, got, want)
		}
	}
	if got := Language(filepath.Join(dir, "missing.cfg")); got != "en" {
		t.Errorf("missing file: %q", got)
	}
}

type sender struct {
	mu   sync.Mutex
	on   bool
	sent []notify.Event
}

func (s *sender) Enabled() bool { return s.on }

func (s *sender) Send(_ context.Context, e notify.Event) []notify.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, e)
	return nil
}

func TestAWatcherReadsTheAppsOnlyWhileAChannelIsOn(t *testing.T) {
	checks := 0
	seen := []board.Seen{up()}
	s := &sender{}
	w := &Watcher{
		Check:    func(context.Context, string) []board.Seen { checks++; return seen },
		Channels: s,
		Texts:    func() (string, Texts) { return "en", en },
	}
	ctx := context.Background()
	w.step(ctx)
	if checks != 0 {
		t.Fatalf("read the apps %d times with every channel off", checks)
	}
	s.on = true
	w.step(ctx)
	seen = []board.Seen{up(need("n1", status.SeverityWarn, "x"))}
	w.step(ctx)
	if len(s.sent) != 1 || !strings.Contains(s.sent[0].Title, "needs you") {
		t.Fatalf("sent %+v", s.sent)
	}
	s.on = false
	w.step(ctx)
	s.on = true
	seen = []board.Seen{up(need("n1", status.SeverityWarn, "x"), need("n2", status.SeverityWarn, "y"))}
	w.step(ctx)
	if len(s.sent) != 1 {
		t.Errorf("a watcher switched on again sent before it learned: %+v", s.sent[1:])
	}
}

func TestAppTextsReachTheChannelsAsShortPlainText(t *testing.T) {
	var tr Tracker
	s := board.Seen{Source: "s1", Name: "Al<b>pha\x07"}
	tr.Next([]board.Seen{s}, en)
	s.Needs = []status.Need{need("n1", status.SeverityWarn, "<img src=x onerror=alert(1)>\n\\x3ca href=\"https://evil\"\\x3e"+strings.Repeat("x", 1000))}
	got := tr.Next([]board.Seen{s}, en)
	if len(got) != 1 {
		t.Fatalf("sent %q", titles(got))
	}
	for _, v := range []string{got[0].Title, got[0].Body, got[0].Fields["app"]} {
		if strings.ContainsAny(v, "<>\x07\n\\\"") {
			t.Errorf("%q keeps markup or control characters", v)
		}
	}
	if n := len([]rune(got[0].Body)); n > maxText {
		t.Errorf("body has %d characters", n)
	}
}

// clock is a Tracker whose time moves only when the test says so.
func clock() (*Tracker, func(time.Duration)) {
	now := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	tr := &Tracker{now: func() time.Time { return now }}
	return tr, func(d time.Duration) { now = now.Add(d) }
}

func TestANeedThatComesBackIsToldAgainOnlyAfterAWhile(t *testing.T) {
	tr, wait := clock()
	n := need("n1", status.SeverityWarn, "x")
	tr.Next([]board.Seen{up()}, en)
	tr.Next([]board.Seen{up(n)}, en)
	tr.Next([]board.Seen{up()}, en)
	wait(time.Hour)
	if got := tr.Next([]board.Seen{up(n)}, en); len(got) != 0 {
		t.Fatalf("a need that came back within the hour was told again: %q", titles(got))
	}
	tr.Next([]board.Seen{up()}, en)
	wait(needMemory)
	if got := tr.Next([]board.Seen{up(n)}, en); len(got) != 1 {
		t.Errorf("a need back after %s: %q", needMemory, titles(got))
	}
}

func TestAnAppIsToldBackAfterTwoAnswersWithoutItsOldNeeds(t *testing.T) {
	var tr Tracker
	n := need("n1", status.SeverityWarn, "x")
	tr.Next([]board.Seen{up(n)}, en)
	for range downChecks {
		tr.Next([]board.Seen{{Source: "s1", Name: "Alpha", Down: true, Needs: []status.Need{n}}}, en)
	}
	if got := tr.Next([]board.Seen{up(n)}, en); len(got) != 0 {
		t.Fatalf("one answer was enough: %q", titles(got))
	}
	got := tr.Next([]board.Seen{up(n)}, en)
	if len(got) != 1 || got[0].Kind != KindBack || got[0].Title != "Alpha is reachable again" || got[0].Severity != notify.SeveritySuccess {
		t.Errorf("sent %+v", got)
	}
}

func TestAFlappingAppIsToldDownOnlyOnceInAWhile(t *testing.T) {
	tr, wait := clock()
	tr.Next([]board.Seen{up()}, en)
	cycle := func() []notify.Event {
		var out []notify.Event
		for range downChecks {
			out = append(out, tr.Next([]board.Seen{down()}, en)...)
			wait(time.Minute)
		}
		for range upChecks {
			out = append(out, tr.Next([]board.Seen{up()}, en)...)
			wait(time.Minute)
		}
		return out
	}
	if got := cycle(); len(got) != 2 {
		t.Fatalf("first outage: %q", titles(got))
	}
	if got := cycle(); len(got) != 0 {
		t.Fatalf("an outage right after the last one: %q", titles(got))
	}
	wait(quietAfterBack)
	if got := cycle(); len(got) != 2 {
		t.Errorf("an outage after the quiet time: %q", titles(got))
	}
}

func TestAnAppCanRaiseOnlyAFewNeedsAnHour(t *testing.T) {
	tr, wait := clock()
	tr.Next([]board.Seen{up()}, en)
	var needs []status.Need
	for i := range 50 {
		needs = append(needs, need(strings.Repeat("n", i+1), status.SeverityWarn, "x"))
	}
	if got := tr.Next([]board.Seen{up(needs...)}, en); len(got) != needsPerHour {
		t.Fatalf("sent %d events, want %d", len(got), needsPerHour)
	}
	wait(30 * time.Minute)
	if got := tr.Next([]board.Seen{up(needs...)}, en); len(got) != 0 {
		t.Fatalf("within the hour: %d events", len(got))
	}
	wait(31 * time.Minute)
	if got := tr.Next([]board.Seen{up(needs...)}, en); len(got) != needsPerHour {
		t.Errorf("an hour later: %d events, want %d", len(got), needsPerHour)
	}
}
