package journal_test

import (
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/notify"
	"github.com/junkerderprovinz/parleyport/kit/status"
	"github.com/junkerderprovinz/parleyport/kit/statustest"

	"github.com/junkerderprovinz/tollroll/internal/alert"
	"github.com/junkerderprovinz/tollroll/internal/fortress"
	"github.com/junkerderprovinz/tollroll/internal/journal"
)

var langDir = filepath.Join("..", "..", "plugin", "src", "tollroll", "usr", "local", "emhttp", "plugins", "tollroll", "lang")

// The fortress serves the journal through status.Handler, which leaves the
// caller's identity to the fortress, so the test stands in for it with a
// token of its own.
func TestStatusContract(t *testing.T) {
	j := &journal.Journal{
		App:   status.App{ID: fortress.AppID, Name: "TollRoll", Version: "1.2.1", Icon: fortress.Icon},
		Every: time.Minute,
		Texts: func(lang string) map[string]string { return alert.LoadTexts(langDir, lang) },
	}
	j.SetInstance(status.Instance{ID: "m1", Name: "Tower"})
	j.Checked(time.Now())
	j.Told(notify.Event{Kind: "app.unreachable", Fields: map[string]string{"app": "ShipLog"}},
		[]notify.Result{{Channel: "a", Kind: "matrix"}, {Channel: "b", Kind: "discord", Err: fmt.Errorf("refused")}})
	j.Delivered("BombVault")

	member := func(r *http.Request) error {
		if r.Header.Get("Authorization") != "Bearer member" {
			return status.ErrUnauthenticated
		}
		return nil
	}
	statustest.Run(t, status.Handler(j, member), statustest.Options{
		Authenticate: func(r *http.Request) { r.Header.Set("Authorization", "Bearer member") },
	})
}
