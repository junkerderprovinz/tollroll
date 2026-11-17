package channels

import (
	"strings"
	"testing"

	"github.com/junkerderprovinz/parleyport/kit/notify"
)

func TestAnUnraidNotificationGoesOutWithNoChannelOn(t *testing.T) {
	s, _, r := open(t, `[{"id":"apprise","kind":"apprise","enabled":false,"apprise":{"url":"http://192.0.2.10:8000/notify/x"}}]`)
	res := s.Unraid(t.Context(), notify.Event{Kind: "host.varlog_full", Severity: notify.SeverityWarning, Title: "/var/log is almost full", Body: "92% full"})
	if !res.OK() {
		t.Fatalf("%+v", res)
	}
	if len(r.calls) != 1 || r.calls[0][0] != notify.UnraidNotifyScript || !strings.Contains(strings.Join(r.calls[0], " "), "/var/log is almost full") {
		t.Errorf("%q", r.calls)
	}
}
