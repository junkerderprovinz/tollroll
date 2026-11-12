package board

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/junkerderprovinz/parleyport/kit/status"
)

func TestAMembersStatusHasItsReportAndItsLatestLinesFirst(t *testing.T) {
	w := newWorld(t)
	p := 0.4
	rep := report("alpha", "Alpha", "1.0.0", status.StateWarning)
	rep.App.UI = "http://cellar:3000"
	rep.NeedsYou = []status.Need{{ID: "n", Severity: status.SeverityWarn, Text: strings.Repeat("ä", 5000), Link: "http://cellar:3000/need"}}
	rep.Work = []status.Task{{ID: "w", Title: "Copying", Progress: &p}}
	rep.Next = []status.Scheduled{{Title: "Check", At: t0.Add(time.Hour)}}
	w.f.apps["a"] = &fakeApp{rep: rep, log: entries(15, t0)}

	got, err := w.board.Status(context.Background(), Source{ID: "a", Name: "Alpha"}, "de")
	if err != nil {
		t.Fatal(err)
	}
	r := got.Report
	if r.State != "warning" || len(r.NeedsYou) != 1 || r.NeedsYou[0].Severity != "warn" || len(r.Work) != 1 || *r.Work[0].Progress != p || len(r.Next) != 1 {
		t.Fatalf("report %+v", r)
	}
	if n := utf8.RuneCountInString(r.NeedsYou[0].Text); n > maxText {
		t.Errorf("need of %d runes", n)
	}
	if len(got.Log) != statusLines || got.Log[0].Text != "line 15 (de)" || got.Log[statusLines-1].Text != "line 6 (de)" {
		t.Errorf("log %+v", got.Log)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "cellar:3000") {
		t.Errorf("the status carries the app's address: %s", raw)
	}
}

func TestAMemberThatDoesNotAnswerHasNoStatus(t *testing.T) {
	w := newWorld(t)
	if _, err := w.board.Status(context.Background(), Source{ID: "gone"}, ""); err == nil {
		t.Error("a member that does not answer has a status")
	}
}

func TestReadingAMembersStatusPutsNoRowOnTheTile(t *testing.T) {
	w := newWorld(t)
	w.f.apps["t"] = &fakeApp{rep: report("tollroll", "TollRoll", "1.0.0", status.StateOK)}
	if _, err := w.board.Status(context.Background(), Source{ID: "t"}, "en"); err != nil {
		t.Fatal(err)
	}
	if v := w.view(); len(v.Apps) != 0 {
		t.Errorf("rows %+v", v.Apps)
	}
}
