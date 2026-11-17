package host

import (
	"strings"
	"testing"
	"time"

	"github.com/junkerderprovinz/tollroll/internal/alert"
)

const parityCron = `# Generated mover schedule:
0 */2 * * * /usr/local/sbin/mover start > /dev/null 2> >(logger -t move)

# Generated parity check schedule:
30 3 1 * * /usr/local/sbin/mdcmd check NOCORRECT &> /dev/null || :
`

func parityText(t *testing.T, s *server, h *Host) string {
	t.Helper()
	l := find(h.Lines("en"), "mdcmd (40): check")
	if l == nil {
		t.Fatal("no line for the parity check")
	}
	return l.Text
}

func TestAParityCheckAtItsScheduledTimeSaysTheScheduleStartedIt(t *testing.T) {
	s := newServer(t)
	write(t, s.root, "/etc/cron.d/root", parityCron)
	s.at = time.Date(2026, 12, 1, 3, 30, 1, 0, time.UTC)
	h := s.host(t)
	s.log(t, "kernel: mdcmd (40): check nocorrect")
	s.look(h)
	en := alert.LoadTexts(langDir, "en")
	if got := parityText(t, s, h); got != en["h_s_parity_schedule"] {
		t.Errorf("%q, want the schedule", got)
	}
}

func TestAParityCheckOutsideTheScheduleWasStartedByHand(t *testing.T) {
	s := newServer(t)
	write(t, s.root, "/etc/cron.d/root", parityCron)
	s.at = time.Date(2026, 12, 1, 14, 12, 0, 0, time.UTC)
	h := s.host(t)
	s.log(t, "kernel: mdcmd (40): check correct")
	s.look(h)
	en := alert.LoadTexts(langDir, "en")
	if got := parityText(t, s, h); got != en["h_s_parity_hand"] {
		t.Errorf("%q, want by hand", got)
	}
}

func TestTheFirstParityCheckAfterAnUncleanShutdownSaysSo(t *testing.T) {
	s := newServer(t)
	h := s.host(t)
	s.log(t,
		"emhttpd: unclean shutdown detected",
		"emhttpd: Starting services...",
		"kernel: mdcmd (40): check correct",
	)
	s.look(h)
	en := alert.LoadTexts(langDir, "en")
	if got := parityText(t, s, h); got != en["h_s_parity_unclean"] {
		t.Errorf("%q, want after an unclean shutdown", got)
	}
	if l := find(h.Lines("en"), "unclean shutdown detected"); l == nil || l.Level != "warn" {
		t.Errorf("unclean line: %+v", l)
	}

	s.log(t, "kernel: mdcmd (41): check correct")
	s.look(h)
	l := find(h.Lines("en"), "mdcmd (41): check")
	if l == nil || l.Text != en["h_s_parity_hand"] {
		t.Errorf("the next check: %+v", l)
	}
}

func TestAResumedParityCheckIsNoNewCheck(t *testing.T) {
	s := newServer(t)
	h := s.host(t)
	s.log(t, "kernel: mdcmd (40): check resume")
	s.look(h)
	l := find(h.Lines("en"), "mdcmd (40): check resume")
	if en := alert.LoadTexts(langDir, "en"); l == nil || l.Text != en["h_s_parity_resume"] {
		t.Errorf("%+v", l)
	}
}

func TestCronFieldsMatchTheirTimes(t *testing.T) {
	at := time.Date(2026, 12, 7, 3, 30, 0, 0, time.UTC) // a Monday
	for _, c := range []struct {
		line string
		want bool
	}{
		{"30 3 * * * /usr/local/sbin/mdcmd check", true},
		{"30 3 1 * * /usr/local/sbin/mdcmd check", false},
		{"30 3 * * 1 /usr/local/sbin/mdcmd check", true},
		{"30 3 * * 0,6 /usr/local/sbin/mdcmd check", false},
		{"*/15 1-4 * 12 * /usr/local/sbin/mdcmd check", true},
		{"0 0 * * * [[ $(date +%e) -le 7 ]] && /usr/local/sbin/mdcmd check", false},
	} {
		f := strings.Fields(c.line)
		if got := cronAt(f[:5], at); got != c.want {
			t.Errorf("%s: %v", c.line, got)
		}
	}
}

func TestASuggestionInTheLogReadsInTheReadersLanguage(t *testing.T) {
	s := newServer(t)
	h := s.host(t)
	s.look(h)
	h.Advise("a_health")
	de := alert.LoadTexts(langDir, "de")
	want := strings.ReplaceAll(de["h_advice"], "{text}", de["a_health"])
	var got string
	for _, l := range h.Lines("de") {
		if l.Text == want {
			got = l.Text
		}
	}
	if got == "" || de["a_health"] == "" {
		t.Errorf("no line %q", want)
	}
}
