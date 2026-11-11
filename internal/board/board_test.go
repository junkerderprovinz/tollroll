package board

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/junkerderprovinz/parleyport/kit/status"
)

var t0 = time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)

var errUnreachable = errors.New("unreachable")

type fakeApp struct {
	rep  status.Report
	err  error
	log  []status.LogEntry
	logs []string // the after cursors the log was asked for
}

type fake struct {
	mu    sync.Mutex
	apps  map[string]*fakeApp
	langs []string
}

func (f *fake) Report(_ context.Context, src Source, lang string) (status.Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.langs = append(f.langs, lang)
	a := f.apps[src.ID]
	if a == nil {
		return status.Report{}, errUnreachable
	}
	if a.err != nil {
		return status.Report{}, a.err
	}
	rep := a.rep
	if lang == "de" {
		rep.App.Name += " (de)"
	}
	return rep, nil
}

func (f *fake) Log(_ context.Context, src Source, after, lang string, limit int) (status.LogPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.apps[src.ID]
	a.logs = append(a.logs, after)
	from := 0
	if after != "" {
		n, _ := strconv.Atoi(after)
		from = n
	}
	page := status.LogPage{Next: after}
	for _, e := range a.log {
		n, _ := strconv.Atoi(e.ID)
		if n > from && len(page.Entries) < limit {
			if lang == "de" {
				e.Text += " (de)"
			}
			page.Entries = append(page.Entries, e)
			page.Next = e.ID
		}
	}
	return page, nil
}

func report(id, name, version string, state status.State) status.Report {
	return status.Report{Contract: 1, App: status.App{ID: id, Name: name, Version: version}, State: state}
}

func entries(n int, start time.Time) []status.LogEntry {
	var out []status.LogEntry
	for i := 1; i <= n; i++ {
		out = append(out, status.LogEntry{ID: strconv.Itoa(i), At: start.Add(time.Duration(i) * time.Minute), Level: status.SeverityInfo, Text: fmt.Sprintf("line %d", i)})
	}
	return out
}

type world struct {
	t     *testing.T
	f     *fake
	srcs  []Source
	now   time.Time
	board *Board
}

func newWorld(t *testing.T) *world {
	w := &world{t: t, f: &fake{apps: map[string]*fakeApp{}}, now: t0}
	w.board = New(func() []Source { return w.srcs }, w.f)
	w.board.now = func() time.Time { return w.now }
	return w
}

// add puts member id on the list, called "app on id" until its app answers.
func (w *world) add(id string, a *fakeApp) {
	w.srcs = append(w.srcs, Source{ID: id, Name: "app on " + id})
	if a != nil {
		w.f.apps[id] = a
	}
}

func (w *world) view() View {
	w.t.Helper()
	return w.board.View(context.Background(), "en")
}

func (w *world) later() { w.now = w.now.Add(time.Minute) }

func TestAnAppGetsARow(t *testing.T) {
	w := newWorld(t)
	w.add("a", &fakeApp{rep: report("alpha", "Alpha", "1.0.0", status.StateOK)})
	v := w.view()
	if len(v.Apps) != 1 {
		t.Fatalf("%d rows", len(v.Apps))
	}
	a := v.Apps[0]
	if a.Key != "alpha" || a.Name != "Alpha" || a.Version != "1.0.0" || a.State != "ok" || a.Instances != 1 {
		t.Fatalf("row %+v", a)
	}
}

func TestTwoInstancesOfAnAppShareARowWithTheWorseState(t *testing.T) {
	w := newWorld(t)
	w.add("a", &fakeApp{rep: report("alpha", "Alpha", "1.0.0", status.StateOK)})
	w.add("b", &fakeApp{rep: report("alpha", "Alpha", "1.0.0", status.StateOverdue)})
	v := w.view()
	if len(v.Apps) != 1 || v.Apps[0].Instances != 2 || v.Apps[0].State != "overdue" {
		t.Fatalf("rows %+v", v.Apps)
	}
}

func TestAMemberNeverReachedShowsTheNameOfItsApp(t *testing.T) {
	w := newWorld(t)
	w.srcs = append(w.srcs, Source{ID: "m1", Name: "Alpha"})
	v := w.view()
	if len(v.Apps) != 1 || v.Apps[0].Name != "Alpha" || v.Apps[0].State != "unreachable" || v.Apps[0].UI != "" {
		t.Fatalf("rows %+v", v.Apps)
	}
	if seen := w.board.Check(context.Background(), "en"); len(seen) != 1 || seen[0].Name != "Alpha" || !seen[0].Down {
		t.Fatalf("check %+v", seen)
	}
}

func TestAnAppThatStopsAnsweringKeepsItsName(t *testing.T) {
	w := newWorld(t)
	a := &fakeApp{rep: report("alpha", "Alpha", "1.0.0", status.StateOK)}
	w.add("a", a)
	w.view()
	a.err = errUnreachable
	w.later()
	v := w.view()
	if v.Apps[0].Name != "Alpha" || v.Apps[0].Version != "1.0.0" || v.Apps[0].State != "unreachable" {
		t.Fatalf("row %+v", v.Apps[0])
	}
}

func TestAnUnreachableInstanceOutranksAWarning(t *testing.T) {
	w := newWorld(t)
	w.add("a", &fakeApp{rep: report("alpha", "Alpha", "1.0.0", status.StateWarning)})
	b := &fakeApp{rep: report("alpha", "Alpha", "1.0.0", status.StateOK)}
	w.add("b", b)
	w.view()
	b.err = errUnreachable
	w.later()
	if got := w.view().Apps[0].State; got != "unreachable" {
		t.Fatalf("state %q", got)
	}
}

func TestTheMostUrgentNeedIsShown(t *testing.T) {
	w := newWorld(t)
	rep := report("alpha", "Alpha", "1.0.0", status.StateFailed)
	rep.NeedsYou = []status.Need{
		{ID: "a", Severity: status.SeverityInfo, Text: "an update waits"},
		{ID: "b", Severity: status.SeverityError, Text: "the backup failed"},
	}
	rep.Work = []status.Task{{ID: "w", Title: "Container: web"}}
	w.add("a", &fakeApp{rep: rep})
	a := w.view().Apps[0]
	if a.Need == nil || a.Need.Text != "the backup failed" || a.Need.Severity != "error" || a.Work != nil {
		t.Fatalf("row %+v", a)
	}
}

func TestRunningWorkIsShownWhenNothingNeedsYou(t *testing.T) {
	w := newWorld(t)
	rep := report("alpha", "Alpha", "1.0.0", status.StateOK)
	p := 0.42
	rep.Work = []status.Task{{ID: "w", Title: "Container: web", Progress: &p}}
	w.add("a", &fakeApp{rep: rep})
	a := w.view().Apps[0]
	if a.Work == nil || a.Work.Title != "Container: web" || a.Work.Progress == nil || *a.Work.Progress != 0.42 {
		t.Fatalf("row %+v", a)
	}
}

func TestRowsAreSortedByName(t *testing.T) {
	w := newWorld(t)
	w.add("z", &fakeApp{rep: report("zulu", "zulu", "1", status.StateOK)})
	w.add("a", &fakeApp{rep: report("bravo", "Bravo", "1", status.StateOK)})
	w.add("m", &fakeApp{rep: report("alpha", "alpha", "1", status.StateOK)})
	v := w.view()
	got := []string{v.Apps[0].Key, v.Apps[1].Key, v.Apps[2].Key}
	if fmt.Sprint(got) != "[alpha bravo zulu]" {
		t.Fatalf("order %v", got)
	}
}

func TestTheLogMixesAllAppsByTime(t *testing.T) {
	w := newWorld(t)
	w.add("a", &fakeApp{rep: report("alpha", "Alpha", "1", status.StateOK), log: entries(2, t0)})
	w.add("b", &fakeApp{rep: report("bravo", "Bravo", "1", status.StateOK), log: entries(2, t0.Add(30*time.Second))})
	v := w.view()
	var got []string
	for _, l := range v.Log {
		got = append(got, l.App+":"+l.Text)
	}
	if fmt.Sprint(got) != "[alpha:line 1 bravo:line 1 alpha:line 2 bravo:line 2]" {
		t.Fatalf("log %v", got)
	}
}

func TestTheLogIsReadOnFromItsCursor(t *testing.T) {
	w := newWorld(t)
	a := &fakeApp{rep: report("alpha", "Alpha", "1", status.StateOK), log: entries(3, t0)}
	w.add("a", a)
	w.view()
	a.log = entries(4, t0)
	w.later()
	v := w.view()
	if len(v.Log) != 4 {
		t.Fatalf("%d lines, want 4", len(v.Log))
	}
	if last := a.logs[len(a.logs)-1]; last != "3" {
		t.Fatalf("second read started after %q, want 3", last)
	}
}

func TestALongLogIsPagedAndTrimmed(t *testing.T) {
	w := newWorld(t)
	a := &fakeApp{rep: report("alpha", "Alpha", "1", status.StateOK), log: entries(450, t0)}
	w.add("a", a)
	v := w.view()
	if len(v.Log) != perSource {
		t.Fatalf("%d lines, want %d", len(v.Log), perSource)
	}
	if v.Log[len(v.Log)-1].Text != "line 450" {
		t.Fatalf("last line %q", v.Log[len(v.Log)-1].Text)
	}
}

func TestAViewIsReusedForAWhile(t *testing.T) {
	w := newWorld(t)
	w.add("a", &fakeApp{rep: report("alpha", "Alpha", "1", status.StateOK)})
	w.view()
	w.now = w.now.Add(ttl / 2)
	w.view()
	if n := len(w.f.langs); n != 1 {
		t.Fatalf("%d reads, want 1", n)
	}
}

func TestEachLanguageHasItsOwnTexts(t *testing.T) {
	w := newWorld(t)
	w.add("a", &fakeApp{rep: report("alpha", "Alpha", "1", status.StateOK), log: entries(1, t0)})
	en := w.board.View(context.Background(), "en")
	de := w.board.View(context.Background(), "de")
	if en.Apps[0].Name != "Alpha" || de.Apps[0].Name != "Alpha (de)" || de.Log[0].Text != "line 1 (de)" || en.Log[0].Text != "line 1" {
		t.Fatalf("en %+v / de %+v", en, de)
	}
}

func TestOnlyWebLinksReachTheTile(t *testing.T) {
	w := newWorld(t)
	bad := report("alpha", "Alpha", "1", status.StateOK)
	bad.App.UI = "javascript:alert(1)"
	good := report("bravo", "Bravo", "1", status.StateOK)
	good.App.UI = "https://bravo.lan/"
	w.add("alpha", &fakeApp{rep: bad})
	w.add("bravo", &fakeApp{rep: good})
	w.add("charlie", &fakeApp{rep: report("charlie", "Charlie", "1", status.StateOK)})
	v := w.view()
	if v.Apps[0].UI != "" || v.Apps[1].UI != "https://bravo.lan/" || v.Apps[2].UI != "" {
		t.Fatalf("links %q %q %q", v.Apps[0].UI, v.Apps[1].UI, v.Apps[2].UI)
	}
}

func TestARemovedSourceLeavesTheBoard(t *testing.T) {
	w := newWorld(t)
	w.add("a", &fakeApp{rep: report("alpha", "Alpha", "1", status.StateOK), log: entries(1, t0)})
	w.view()
	w.srcs = nil
	w.later()
	v := w.view()
	if len(v.Apps) != 0 || len(v.Log) != 0 {
		t.Fatalf("view %+v", v)
	}
}

func TestAChangedListIsReadAtOnce(t *testing.T) {
	w := newWorld(t)
	w.add("a", &fakeApp{rep: report("alpha", "Alpha", "1", status.StateOK)})
	w.view()
	w.add("b", &fakeApp{rep: report("bravo", "Bravo", "1", status.StateOK)})
	if n := len(w.view().Apps); n != 2 {
		t.Fatalf("%d rows right after a source was added", n)
	}
}

type slow struct{ fake }

func (s *slow) Log(ctx context.Context, _ Source, _, _ string, _ int) (status.LogPage, error) {
	<-ctx.Done()
	return status.LogPage{}, ctx.Err()
}

func TestASlowAppCannotHoldTheBoard(t *testing.T) {
	s := &slow{fake: fake{apps: map[string]*fakeApp{"a": {rep: report("alpha", "Alpha", "1", status.StateOK)}}}}
	b := New(func() []Source { return []Source{{ID: "a", Name: "Alpha"}} }, s)
	b.budget = 50 * time.Millisecond
	start := time.Now()
	v := b.View(context.Background(), "en")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("view took %v", d)
	}
	if len(v.Apps) != 1 || v.Apps[0].State != "ok" {
		t.Fatalf("view %+v", v)
	}
}

func TestOverlongTextsAreCut(t *testing.T) {
	w := newWorld(t)
	rep := report("alpha", strings.Repeat("N", 500), strings.Repeat("9", 500), status.StateWarning)
	rep.NeedsYou = []status.Need{{ID: "n", Severity: status.SeverityWarn, Text: strings.Repeat("ä", 5000)}}
	log := entries(1, t0)
	log[0].Text = strings.Repeat("x", 100000)
	w.add("a", &fakeApp{rep: rep, log: log})
	v := w.view()
	a := v.Apps[0]
	if n := utf8.RuneCountInString(a.Name); n > maxName {
		t.Errorf("name of %d runes", n)
	}
	if n := utf8.RuneCountInString(a.Version); n > maxName {
		t.Errorf("version of %d runes", n)
	}
	if n := utf8.RuneCountInString(a.Need.Text); n > maxText {
		t.Errorf("need of %d runes", n)
	}
	if n := utf8.RuneCountInString(v.Log[0].Text); n > maxText {
		t.Errorf("line of %d runes", n)
	}
	if !utf8.ValidString(a.Need.Text) {
		t.Error("cut inside a character")
	}
}

func TestHugeIDsAndUnknownLevelsAreTamed(t *testing.T) {
	w := newWorld(t)
	rep := report(strings.Repeat("i", 5000), "Alpha", "1", "exploded")
	log := entries(1, t0)
	log[0].ID = strings.Repeat("7", 5000)
	log[0].Level = "<img>"
	w.add("a", &fakeApp{rep: rep, log: log})
	v := w.view()
	if n := len(v.Apps[0].Key); n > maxName {
		t.Errorf("key of %d bytes", n)
	}
	if v.Apps[0].State != Unreachable {
		t.Errorf("unknown state passed as %q", v.Apps[0].State)
	}
	if len(v.Log[0].ID) > 2*maxName+2 || len(v.Log[0].App) > maxName || v.Log[0].Level != "info" {
		t.Errorf("line %+v", v.Log[0])
	}
}

func TestOnlyAFewLanguagesAreKept(t *testing.T) {
	w := newWorld(t)
	w.add("a", &fakeApp{rep: report("alpha", "Alpha", "1", status.StateOK)})
	for _, l := range []string{"aa", "bb", "cc", "dd", "ee", "ff", "gg"} {
		w.board.View(context.Background(), l)
	}
	if n := len(w.board.cached); n > maxLangs {
		t.Errorf("%d cached views", n)
	}
	for _, s := range w.board.state {
		if n := len(s.reports); n > maxLangs {
			t.Errorf("%d reports kept", n)
		}
	}
}

func TestALinkWithQuotesOrMarkupIsDropped(t *testing.T) {
	w := newWorld(t)
	for i, ui := range []string{`http://a.lan/x"><img src=x onerror=alert(1)>`, `http://a.lan/x'y`, `http://a.lan/a b`, `http://a.lan/a\b`} {
		rep := report(fmt.Sprint("app", i), "App", "1", status.StateOK)
		rep.App.UI = ui
		w.add(fmt.Sprint("app", i), &fakeApp{rep: rep})
	}
	for _, a := range w.view().Apps {
		if a.UI != "" {
			t.Errorf("%s links to %q", a.Key, a.UI)
		}
	}
}
