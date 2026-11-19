package board

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/status"
)

func TestTheLogOfAnAppIsSearchedForAName(t *testing.T) {
	f := &fake{apps: map[string]*fakeApp{"m1": {
		rep: status.Report{App: status.App{ID: "bombvault", Name: "BombVault"}, State: status.StateOK},
		log: []status.LogEntry{{ID: "1", At: t0, Level: status.SeverityInfo, Text: "Pruned snapshot appdata@daily-2026-11-01"}},
	}}}
	b := New(func() []Source { return []Source{{ID: "m1"}} }, f)
	if !b.Mentions(context.Background(), "daily-2026-11-01") {
		t.Error("the pruned snapshot is not found")
	}
	if b.Mentions(context.Background(), "daily-2026-11-02") || b.Mentions(context.Background(), "") {
		t.Error("found what no app logged")
	}
}

func TestAChattyAppLeavesTheOthersTheirNewestLines(t *testing.T) {
	var chatty []status.LogEntry
	for i := range 2 * maxLines {
		chatty = append(chatty, status.LogEntry{ID: strconv.Itoa(i + 1), At: t0.Add(time.Duration(i) * time.Second), Level: status.SeverityInfo, Text: "chatter"})
	}
	f := &fake{apps: map[string]*fakeApp{
		"m1": {rep: status.Report{App: status.App{ID: "chatty", Name: "Chatty"}, State: status.StateOK}, log: chatty},
		"m2": {rep: status.Report{App: status.App{ID: "calm", Name: "Calm"}, State: status.StateOK},
			log: []status.LogEntry{{ID: "1", At: t0.Add(-time.Hour), Level: status.SeverityWarn, Text: "an hour ago"}}},
	}}
	b := New(func() []Source { return []Source{{ID: "m1"}, {ID: "m2"}} }, f)
	b.Local = locals{lines: func() []Line {
		var out []Line
		for i := range maxLines {
			out = append(out, Line{ID: "unraid:" + strconv.Itoa(i), At: t0.Add(time.Duration(i) * time.Millisecond), App: "unraid", Level: "info", Text: "noise"})
		}
		return out
	}}
	v := b.View(context.Background(), "en")
	n := map[string]int{}
	for _, l := range v.Log {
		n[l.App]++
	}
	if n["calm"] != 1 || n["unraid"] < keepEach || len(v.Log) > maxLines+2*keepEach {
		t.Errorf("%v of %d", n, len(v.Log))
	}
	for i := 1; i < len(v.Log); i++ {
		if v.Log[i].At.Before(v.Log[i-1].At) {
			t.Fatal("the log is out of order")
		}
	}
}

type locals struct{ lines func() []Line }

func (l locals) Rows(string) []App   { return nil }
func (l locals) Lines(string) []Line { return l.lines() }
