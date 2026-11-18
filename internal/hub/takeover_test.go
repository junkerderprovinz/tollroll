package hub_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/notify"
	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/hub"
	"github.com/junkerderprovinz/tollroll/internal/journal"
)

type on struct{}

func (on) Enabled() bool { return true }

func (on) Send(context.Context, notify.Event) []notify.Result { return nil }

// The members read TollRoll's report whenever they ask, so the switch has to
// show in the very next report, on and off.
func TestTheStatusSaysAtOnceWhetherTheHubTakesOver(t *testing.T) {
	st := hub.OpenStore(filepath.Join(t.TempDir(), "hub.json"))
	h := hub.New(hub.Options{Channels: on{}, Settings: st, Texts: func() (string, map[string]string) { return "en", nil }})
	j := &journal.Journal{App: status.App{ID: "tollroll", Name: "TollRoll"}, Every: time.Minute,
		Texts: func(string) map[string]string { return map[string]string{} }, Hub: h.TakesOver}
	takes := func() bool {
		rep, err := j.Report(context.Background(), "en")
		if err != nil {
			t.Fatal(err)
		}
		return slices.Contains(rep.Capabilities, hub.Capability)
	}
	if takes() {
		t.Fatal("before the switch")
	}
	s := st.Get()
	s.TakeOver = true
	if err := st.Set(s); err != nil {
		t.Fatal(err)
	}
	if !takes() {
		t.Error("right after switching on")
	}
	s.TakeOver = false
	if err := st.Set(s); err != nil {
		t.Fatal(err)
	}
	if takes() {
		t.Error("right after switching off")
	}
}
