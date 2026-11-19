package board

import (
	"context"
	"testing"

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
