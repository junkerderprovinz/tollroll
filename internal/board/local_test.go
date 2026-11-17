package board

import (
	"fmt"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/status"
)

type server struct {
	langs []string
}

func (s *server) Rows(lang string) []App {
	s.langs = append(s.langs, lang)
	return []App{{Key: "unraid", Name: "Unraid", State: "ok", Instances: 1, Local: true}}
}

func (s *server) Lines(string) []Line {
	return []Line{
		{ID: "unraid:1", At: t0.Add(90 * time.Second), App: "unraid", Level: "warn", Text: "Disk 2 had a read error.", Raw: "kernel: md: disk2 read error"},
		{ID: "unraid:2", At: t0.Add(150 * time.Second), App: "unraid", Level: "info", Text: "avahi-daemon: Joining", Quiet: true},
	}
}

func TestTheServersOwnRowsComeAfterTheAppsAndItsLinesJoinTheLog(t *testing.T) {
	w := newWorld(t)
	w.add("a", &fakeApp{rep: report("zeta", "Zeta", "1.0.0", status.StateOK), log: entries(3, t0)})
	s := &server{}
	w.board.Local = s
	v := w.board.View(t.Context(), "de")
	if len(v.Apps) != 2 || v.Apps[0].Key != "zeta" || v.Apps[1].Key != "unraid" || !v.Apps[1].Local {
		t.Fatalf("rows %+v", v.Apps)
	}
	if len(s.langs) != 1 || s.langs[0] != "de" {
		t.Errorf("asked in %v", s.langs)
	}
	var got []string
	for _, l := range v.Log {
		got = append(got, l.ID)
	}
	if want := "[a:1 unraid:1 a:2 unraid:2 a:3]"; fmt.Sprint(got) != want {
		t.Errorf("log %v, want %s", got, want)
	}
	if l := v.Log[1]; l.Raw != "kernel: md: disk2 read error" || l.Quiet {
		t.Errorf("%+v", l)
	}
	if !v.Log[3].Quiet {
		t.Error("the routine line lost its mark")
	}
}

func TestTheServersRowsNeverReachTheNotifications(t *testing.T) {
	w := newWorld(t)
	w.add("a", &fakeApp{rep: report("zeta", "Zeta", "1.0.0", status.StateOK)})
	w.board.Local = &server{}
	for _, s := range w.board.Check(t.Context(), "en") {
		if s.Source != "a" {
			t.Errorf("checked %+v", s)
		}
	}
}

func TestTheServersRowsShowWhileTollRollIsInNoFortress(t *testing.T) {
	w := newWorld(t)
	w.board.Local = &server{}
	v := w.view()
	if len(v.Apps) != 1 || v.Apps[0].Key != "unraid" || len(v.Log) != 2 {
		t.Fatalf("rows %+v, log %+v", v.Apps, v.Log)
	}
}
