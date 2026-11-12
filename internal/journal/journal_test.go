package journal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/notify"
	"github.com/junkerderprovinz/parleyport/kit/status"
)

var t0 = time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)

var words = map[string]map[string]string{
	"en": {
		"r_next_check":  "Checking your apps",
		"r_told_down":   "Told you that {name} is not reachable",
		"r_told_back":   "Told you that {name} is reachable again",
		"r_told_need":   "Told you that {name} needs you",
		"r_send_failed": "Could not send a notification through {channel}",
		"r_removed":     "Took {app} on {device} out of the fortress",
		"s_n_matrix":    "Matrix",
	},
	"de": {
		"r_told_down": "Gemeldet: {name} ist nicht erreichbar",
	},
}

func newJournal() (*Journal, *time.Time) {
	now := t0
	j := &Journal{
		App:   status.App{ID: "tollroll", Name: "TollRoll", Version: "1.0.0"},
		Every: time.Minute,
		Texts: func(lang string) map[string]string {
			out := map[string]string{}
			for k, v := range words["en"] {
				out[k] = v
			}
			for k, v := range words[lang] {
				out[k] = v
			}
			return out
		},
		Now: func() time.Time { return now },
	}
	j.SetInstance(status.Instance{ID: "m1", Name: "Bottich"})
	return j, &now
}

func event(kind, name string) notify.Event {
	return notify.Event{Kind: kind, Title: name + " title", Fields: map[string]string{"app": name}}
}

func ok() []notify.Result { return []notify.Result{{Channel: "a", Kind: "matrix"}} }

func TestAQuietTollRollReportsItselfAsRunning(t *testing.T) {
	j, _ := newJournal()
	rep, err := j.Report(context.Background(), "en")
	if err != nil {
		t.Fatal(err)
	}
	if rep.App.ID != "tollroll" || rep.App.Version != "1.0.0" || rep.Instance.Name != "Bottich" || rep.State != status.StateOK {
		t.Fatalf("report %+v", rep)
	}
	if len(rep.NeedsYou)+len(rep.Work)+len(rep.Next) != 0 {
		t.Errorf("a quiet TollRoll reports %+v", rep)
	}
}

func TestWhileItWatchesItSaysWhenItChecksNext(t *testing.T) {
	j, now := newJournal()
	j.Checked(t0)
	*now = t0.Add(10 * time.Second)
	rep, _ := j.Report(context.Background(), "en")
	if len(rep.Next) != 1 || rep.Next[0].Title != "Checking your apps" || !rep.Next[0].At.Equal(t0.Add(time.Minute)) || rep.Next[0].Kind != status.KindCheck {
		t.Fatalf("next %+v", rep.Next)
	}
	*now = t0.Add(5 * time.Minute)
	if rep, _ := j.Report(context.Background(), "en"); len(rep.Next) != 0 {
		t.Errorf("a TollRoll that stopped checking still plans %+v", rep.Next)
	}
}

func TestTheLogSaysWhatTollRollToldYouInTheReadersLanguage(t *testing.T) {
	j, now := newJournal()
	j.Told(event("app.unreachable", "BombVault"), ok())
	*now = t0.Add(time.Minute)
	j.Told(event("app.reachable", "BombVault"), ok())
	j.Told(event("app.needs_you", "ShipLog"), ok())
	j.Removed("KnightLoader", "Cellar")

	page, err := j.Log(context.Background(), status.LogQuery{Limit: 10, Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Entries {
		got = append(got, string(e.Level)+" "+e.Text)
	}
	want := "warn Told you that BombVault is not reachable|info Told you that BombVault is reachable again|warn Told you that ShipLog needs you|info Took KnightLoader on Cellar out of the fortress"
	if strings.Join(got, "|") != want {
		t.Fatalf("log\n%s\nwant\n%s", strings.Join(got, "|"), want)
	}
	de, _ := j.Log(context.Background(), status.LogQuery{Limit: 1, Lang: "de"})
	if len(de.Entries) != 1 || de.Entries[0].Text != "Gemeldet: BombVault ist nicht erreichbar" {
		t.Errorf("german log %+v", de.Entries)
	}
	rest, _ := j.Log(context.Background(), status.LogQuery{After: de.Next, Limit: 10, Lang: "en"})
	if len(rest.Entries) != 3 {
		t.Errorf("after the cursor %d entries", len(rest.Entries))
	}
}

func TestAChannelThatFailsNeedsYouUntilItWorksAgain(t *testing.T) {
	j, _ := newJournal()
	j.Told(event("app.unreachable", "BombVault"), []notify.Result{{Channel: "a", Kind: "matrix", Code: "auth", Err: errors.New("refused")}})
	rep, _ := j.Report(context.Background(), "en")
	if rep.State != status.StateWarning || len(rep.NeedsYou) != 1 || rep.NeedsYou[0].Text != "Could not send a notification through Matrix" || rep.NeedsYou[0].Severity != status.SeverityWarn {
		t.Fatalf("report %+v", rep)
	}
	page, _ := j.Log(context.Background(), status.LogQuery{Limit: 10, Lang: "en"})
	if n := len(page.Entries); n != 1 || page.Entries[0].Level != status.SeverityError {
		t.Errorf("log %+v", page.Entries)
	}
	j.Told(event("app.reachable", "BombVault"), ok())
	if rep, _ := j.Report(context.Background(), "en"); rep.State != status.StateOK || len(rep.NeedsYou) != 0 {
		t.Errorf("after it worked again %+v", rep)
	}
}

func TestTheLogKeepsItsNewestLines(t *testing.T) {
	j, _ := newJournal()
	for range maxLog + 20 {
		j.Removed("Alpha", "Cellar")
	}
	page, _ := j.Log(context.Background(), status.LogQuery{Limit: status.MaxLogLimit, Lang: "en"})
	all := len(page.Entries)
	for page.Next != "" && len(page.Entries) == status.MaxLogLimit {
		page, _ = j.Log(context.Background(), status.LogQuery{After: page.Next, Limit: status.MaxLogLimit, Lang: "en"})
		all += len(page.Entries)
	}
	if all != maxLog {
		t.Errorf("%d lines kept, want %d", all, maxLog)
	}
}
