package host

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/notify"

	"github.com/junkerderprovinz/tollroll/internal/board"
)

func watchNeed(t *testing.T, h *Host, app, text string) board.Need {
	t.Helper()
	r := row(h.Rows("en"), app)
	if r != nil {
		for _, n := range r.Needs {
			if strings.Contains(n.Text, text) {
				return n
			}
		}
	}
	t.Fatalf("no need with %q on %s: %+v", text, app, r)
	return board.Need{}
}

func noNeed(t *testing.T, h *Host, app, text string) {
	t.Helper()
	if r := row(h.Rows("en"), app); r != nil {
		for _, n := range r.Needs {
			if strings.Contains(n.Text, text) {
				t.Fatalf("still listed: %+v", n)
			}
		}
	}
}

func burstFrom(s *server, t *testing.T, addr string) {
	for range 5 {
		s.log(t, "sshd-session[2201]: Failed password for root from "+addr+" port 50122 ssh2")
	}
}

func TestAFindingTakenAsKnownIsNotReportedAgain(t *testing.T) {
	s := newServer(t)
	h := s.host(t)
	s.look(h)
	burstFrom(s, t, "198.51.100.7")
	s.look(h)
	n := watchNeed(t, h, Unraid, "198.51.100.7")
	if n.ID == "" || !n.Until.IsZero() {
		t.Fatalf("a finding of the watch: %+v", n)
	}
	if err := h.Mark(n.ID, MarkKnown, ""); err != nil {
		t.Fatal(err)
	}
	noNeed(t, h, Unraid, "198.51.100.7")

	s.at = s.at.Add(2 * time.Hour)
	burstFrom(s, t, "198.51.100.7")
	burstFrom(s, t, "203.0.113.4")
	s.look(h)
	noNeed(t, h, Unraid, "198.51.100.7")
	watchNeed(t, h, Unraid, "203.0.113.4")
	var quiet bool
	for _, l := range h.Lines("en") {
		if l.Quiet && strings.Contains(l.Text, "198.51.100.7") && strings.Contains(l.Text, "Failed sign-ins") {
			quiet = true
		}
	}
	if !quiet {
		t.Error("a known finding leaves no routine line")
	}
}

func TestAnErrorOfTheServerCannotBeMarked(t *testing.T) {
	s := newServer(t)
	h := s.host(t)
	s.look(h)
	s.log(t, "kernel: md: disk2 read error, sector=1953525160")
	s.look(h)
	r := row(h.Rows("en"), Unraid)
	if r.Need == nil || r.Need.ID != "" {
		t.Fatalf("a disk error carries an ID: %+v", r.Need)
	}
	if err := h.Mark("unraid:999", MarkKnown, ""); !errors.Is(err, ErrNoEntry) {
		t.Errorf("an unknown entry: %v", err)
	}
	if err := h.Mark("unraid:1", "maybe", ""); !errors.Is(err, ErrNoEntry) {
		t.Errorf("an unknown mark: %v", err)
	}
}

func TestARealIncidentKeepsItsNoteAndIsToldAtOnceWhenItHappensAgain(t *testing.T) {
	s := newServer(t)
	h := s.host(t)
	s.look(h)
	s.log(t, "sshd-session[2201]: Accepted publickey for root from 198.51.100.9 port 50122 ssh2: ED25519 SHA256:abc")
	s.look(h)
	n := watchNeed(t, h, Unraid, "198.51.100.9")
	marked := s.at
	if err := h.Mark(n.ID, MarkIncident, "  That was not me.\nCheck the keys. "); err != nil {
		t.Fatal(err)
	}
	inc := watchNeed(t, h, Unraid, "198.51.100.9")
	if inc.Note != "That was not me. Check the keys." || !inc.Until.Equal(marked.Add(30*24*time.Hour)) || inc.ID == "" {
		t.Fatalf("%+v", inc)
	}
	if r := row(h.Rows("en"), Unraid); r.State != "ok" || r.Need != nil {
		t.Errorf("an incident on record is nothing new to look at: %+v", r)
	}
	if len(s.sent) != 0 {
		t.Fatalf("told before it happened again: %+v", s.sent)
	}

	s.at = s.at.Add(3 * 24 * time.Hour)
	s.log(t, "sshd-session[2301]: Accepted publickey for root from 198.51.100.9 port 50410 ssh2: ED25519 SHA256:abc")
	s.look(h)
	if len(s.sent) != 1 || s.sent[0].Severity != notify.SeverityCritical || !strings.Contains(s.sent[0].Body, "198.51.100.9") ||
		!strings.Contains(s.sent[0].Body, "That was not me.") {
		t.Fatalf("%+v", s.sent)
	}
	s.at = s.at.Add(time.Hour)
	s.log(t, "sshd-session[2302]: Accepted publickey for root from 198.51.100.9 port 50411 ssh2: ED25519 SHA256:abc")
	s.look(h)
	if len(s.sent) != 1 {
		t.Errorf("a known address again: %+v", s.sent)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.Run(ctx)
	h2 := s.host(t)
	s.look(h2)
	if again := watchNeed(t, h2, Unraid, "198.51.100.9"); again.Note == "" {
		t.Errorf("the incident was lost on a restart: %+v", again)
	}
	s.at = marked.Add(31 * 24 * time.Hour)
	s.look(h2)
	for _, n := range row(h2.Rows("en"), Unraid).Needs {
		if !n.Until.IsZero() {
			t.Errorf("an incident past its 30 days: %+v", n)
		}
	}
}

func TestAnIncidentTakenAsKnownEnds(t *testing.T) {
	s := newServer(t)
	h := s.host(t)
	s.look(h)
	burstFrom(s, t, "198.51.100.7")
	s.look(h)
	n := watchNeed(t, h, Unraid, "198.51.100.7")
	if err := h.Mark(n.ID, MarkIncident, ""); err != nil {
		t.Fatal(err)
	}
	inc := watchNeed(t, h, Unraid, "198.51.100.7")
	if err := h.Mark(inc.ID, MarkKnown, ""); err != nil {
		t.Fatal(err)
	}
	noNeed(t, h, Unraid, "198.51.100.7")
	s.at = s.at.Add(2 * time.Hour)
	burstFrom(s, t, "198.51.100.7")
	s.look(h)
	if len(s.sent) != 0 {
		t.Errorf("told after it was taken as known: %+v", s.sent)
	}
}

func TestAKnownFlashFileIsLeftOutOfLaterChanges(t *testing.T) {
	s := newServer(t)
	write(t, s.root, "/boot/config/ident.cfg", "NAME=\"Tower\"\n")
	write(t, s.root, "/boot/config/disk.cfg", "spindownDelay=\"0\"\n")
	h := s.host(t)
	s.look(h)
	write(t, s.root, "/boot/config/ident.cfg", "NAME=\"Tower2\"\n")
	s.look(h)
	n := watchNeed(t, h, Unraid, "ident.cfg")
	if err := h.Mark(n.ID, MarkKnown, ""); err != nil {
		t.Fatal(err)
	}
	write(t, s.root, "/boot/config/ident.cfg", "NAME=\"Tower3\"\n")
	s.look(h)
	noNeed(t, h, Unraid, "ident.cfg")
	write(t, s.root, "/boot/config/ident.cfg", "NAME=\"Tower4\"\n")
	write(t, s.root, "/boot/config/disk.cfg", "spindownDelay=\"1\"\n")
	s.look(h)
	if got := watchNeed(t, h, Unraid, "disk.cfg"); strings.Contains(got.Text, "ident.cfg") {
		t.Errorf("a known file among the changes: %s", got.Text)
	}
}

func TestARouterFindingCanBeMarkedToo(t *testing.T) {
	s := routerServer(t)
	h := s.routerHost(t)
	s.look(h)
	s.router(t, gateway, "gw", "system,info,account user admin logged in from 203.0.113.5 via winbox")
	s.look(h)
	n := watchNeed(t, h, Router, "203.0.113.5")
	if err := h.Mark(n.ID, MarkKnown, ""); err != nil {
		t.Fatal(err)
	}
	noNeed(t, h, Router, "203.0.113.5")
}
