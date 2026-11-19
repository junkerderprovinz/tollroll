package hub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/notify"
)

func TestMuteHoldsAllButAlertsOnceAndSumsUpWhenItEnds(t *testing.T) {
	e := newEnv(t, Settings{})
	s := e.st.Get()
	s.Mute = e.now.Add(2 * time.Hour)
	if err := e.st.Set(s); err != nil {
		t.Fatal(err)
	}
	e.offer("shiplog", "Update ready", notify.SeverityInfo)
	e.offer("bombvault", "Backup failed", notify.SeverityFailure)
	e.offer("firesquire", "Disk overheating", notify.SeverityCritical)
	e.h.Tick(context.Background())
	if got := e.ch.titles(); len(got) != 1 || got[0] != "Disk overheating" {
		t.Fatalf("while muted: %q", got)
	}
	e.now = e.now.Add(2*time.Hour + time.Minute)
	e.h.Tick(context.Background())
	e.h.Tick(context.Background())
	got := e.ch.sent
	if len(got) != 2 || got[1].Title != "Mute is over" || got[1].Body != "Shiplog: Update ready\nBombvault: Backup failed" {
		t.Fatalf("%+v", got)
	}
	e.offer("shiplog", "Another update", notify.SeverityInfo)
	if n := len(e.ch.sent); n != 3 {
		t.Errorf("muted again the next day: %d sent", n)
	}
}

func TestAMuteThatEndsEarlySaysSoEvenWithNothingHeld(t *testing.T) {
	e := newEnv(t, Settings{})
	s := e.st.Get()
	s.Mute = e.now.Add(8 * time.Hour)
	_ = e.st.Set(s)
	e.h.Tick(context.Background())
	s.Mute = time.Time{}
	_ = e.st.Set(s)
	e.now = e.now.Add(time.Minute)
	e.h.Tick(context.Background())
	if got := e.ch.sent; len(got) != 1 || got[0].Title != "Mute is over" || got[0].Body != "Nothing came" {
		t.Errorf("%+v", got)
	}
}

func TestAllQuietSaysOnceADayThatNothingHappened(t *testing.T) {
	e := newEnv(t, Settings{Calm: true, Digest: Daily{At: "08:00"}})
	e.h.o.Open = func(string) []string { return nil }
	e.at("08:01")
	e.h.Tick(context.Background())
	e.h.Tick(context.Background())
	if got := e.ch.sent; len(got) != 1 || got[0].Title != "All quiet" || got[0].Body != "Nothing happened" {
		t.Fatalf("%+v", got)
	}
	e.now = e.now.Add(2 * time.Hour)
	e.offer("shiplog", "Update ready", notify.SeverityInfo)
	e.now = e.now.Add(22 * time.Hour)
	e.h.Tick(context.Background())
	if got := e.ch.titles(); len(got) != 2 {
		t.Errorf("a day with a message: %q", got)
	}
}

func TestAllQuietLeavesItToTheSummaryAndToOpenPoints(t *testing.T) {
	e := newEnv(t, Settings{Calm: true, Digest: Daily{At: "08:00"}})
	e.at("08:01")
	e.h.Tick(context.Background())
	if got := e.ch.titles(); len(got) != 0 {
		t.Fatalf("something is open: %q", got)
	}
	s := e.st.Get()
	s.Digest.On = true
	_ = e.st.Set(s)
	e.now = e.now.Add(24 * time.Hour)
	e.h.Tick(context.Background())
	if got := e.ch.titles(); len(got) != 1 || got[0] != "Your day" {
		t.Errorf("%q", got)
	}
}

func TestAMuteReachesNoFurtherThanTwoDays(t *testing.T) {
	st := OpenStore(t.TempDir() + "/hub.json")
	s := st.Get()
	s.Mute = time.Now().Add(72 * time.Hour)
	if err := st.Set(s); !errors.Is(err, ErrSettings) {
		t.Errorf("%v", err)
	}
	s.Mute = time.Now().Add(20 * time.Hour)
	if err := st.Set(s); err != nil {
		t.Errorf("%v", err)
	}
}
