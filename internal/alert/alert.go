// Package alert turns what the board sees into notifications: an app that
// stops answering, one that answers again, and every need an app newly
// raises. Each is told once, however often the apps are read, and the hub the
// events go through keeps an app gone wrong from flooding the channels.
package alert

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/junkerderprovinz/parleyport/kit/notify"
	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/board"
)

// The kinds of the events, which a channel's events list can pick from.
const (
	KindDown = "app.unreachable"
	KindBack = "app.reachable"
	KindNeed = "app.needs_you"
)

const (
	// downChecks is how many checks in a row an app must miss before it
	// counts as down, so a restart or one slow answer stays quiet.
	downChecks = 3
	// upChecks is how many answers in a row bring a down app back.
	upChecks = 2
	// quietAfterBack keeps an app that keeps dropping out from sending a
	// pair of messages every few minutes.
	quietAfterBack = 30 * time.Minute
	// needMemory is how long a need that was told stays told, so one that
	// comes and goes is not repeated every time it returns.
	needMemory = 12 * time.Hour
	// maxText cuts what an app wrote to what a notification shows.
	maxText = 300
	// sendFor bounds the delivery of one event, retries included.
	sendFor = 2 * time.Minute
)

// Tracker remembers what was told about each source. The zero value is ready
// to use. A source is learned at its first answer without a word, so a start
// of the service does not repeat every need that is already there.
type Tracker struct {
	sources map[string]*track
	now     func() time.Time
	gone    []Gone
}

// Gone is a need that was told and is over, by the app and the key its
// event carried.
type Gone struct {
	App, Key string
}

type track struct {
	misses, hits int
	down         bool
	quiet        time.Time
	learned      bool
	told         map[string]time.Time
	open         map[string]bool
}

// Next compares seen with what was told before and returns the events to
// send.
func (t *Tracker) Next(seen []board.Seen, tx Texts) []notify.Event {
	if t.sources == nil {
		t.sources = map[string]*track{}
	}
	now := time.Now()
	if t.now != nil {
		now = t.now()
	}
	var out []notify.Event
	t.gone = nil
	keep := map[string]bool{}
	for _, s := range seen {
		keep[s.Source] = true
		tr := t.sources[s.Source]
		if tr == nil {
			tr = &track{told: map[string]time.Time{}}
			t.sources[s.Source] = tr
		}
		out = append(out, tr.next(s, tx, now)...)
		if !s.Down {
			open := map[string]bool{}
			for _, n := range s.Needs {
				open[n.ID] = true
			}
			for id := range tr.open {
				if !open[id] {
					t.gone = append(t.gone, Gone{App: s.App, Key: "need:" + id})
				}
			}
			tr.open = open
		}
	}
	for id := range t.sources {
		if !keep[id] {
			delete(t.sources, id)
		}
	}
	return out
}

func (tr *track) next(s board.Seen, tx Texts, now time.Time) []notify.Event {
	s.Name = Plain(s.Name)
	if s.Down {
		tr.hits = 0
		tr.misses++
		if tr.misses >= downChecks && !tr.down && !now.Before(tr.quiet) {
			tr.down = true
			return []notify.Event{event(KindDown, notify.SeverityWarning, tx.Get("n_down_title", s.Name), tx.Get("n_down_body", s.Name), s, "reach")}
		}
		return nil
	}
	tr.misses = 0
	var out []notify.Event
	if tr.down {
		if tr.hits++; tr.hits < upChecks {
			return nil
		}
		tr.down, tr.hits, tr.quiet = false, 0, now.Add(quietAfterBack)
		out = append(out, event(KindBack, notify.SeveritySuccess, tx.Get("n_back_title", s.Name), tx.Get("n_back_body", s.Name), s, "reach"))
	}
	for id, at := range tr.told {
		if now.Sub(at) >= needMemory {
			delete(tr.told, id)
		}
	}
	for _, n := range s.Needs {
		if _, ok := tr.told[n.ID]; ok {
			continue
		}
		if !tr.learned {
			tr.told[n.ID] = now
			continue
		}
		tr.told[n.ID] = now
		out = append(out, event(KindNeed, severity(n.Severity), tx.Get("n_need_title", s.Name), Plain(n.Text), s, "need:"+n.ID))
	}
	tr.learned = true
	return out
}

// Gone returns the needs the last Next saw end.
func (t *Tracker) Gone() []Gone { return t.gone }

// event names the app, its ID and what the event is about, which the hub
// tells once until it is over.
func event(kind string, sev notify.Severity, title, body string, s board.Seen, key string) notify.Event {
	return notify.Event{Kind: kind, Severity: sev, Title: title, Body: body, Fields: map[string]string{"app": s.Name, "appId": s.App, "key": key}}
}

// Plain makes what an app wrote safe for every channel: one short line with
// no markup. Unraid shows it in the WebGUI and hands it to agents that run it
// through echo -e and paste it into JSON, hence no backslash and no double
// quote either.
func Plain(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '<' || r == '>' || r == '\\':
			return -1
		case r == '"':
			return '\''
		case unicode.IsControl(r):
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxText {
		s = string(r[:maxText-1]) + "…"
	}
	return s
}

func severity(s status.Severity) notify.Severity {
	switch s {
	case status.SeverityError:
		return notify.SeverityFailure
	case status.SeverityWarn:
		return notify.SeverityWarning
	}
	return notify.SeverityInfo
}

// Texts are the strings of one language, English where it has none.
type Texts map[string]string

var langCode = regexp.MustCompile(`^[a-z]{2}$`)

// LoadTexts reads the plugin's language files in dir: English, and lang over
// it.
func LoadTexts(dir, lang string) Texts {
	tx := Texts{}
	for _, l := range []string{"en", lang} {
		if !langCode.MatchString(l) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, l+".json"))
		if err != nil {
			continue
		}
		var m map[string]string
		if json.Unmarshal(b, &m) == nil {
			for k, v := range m {
				tx[k] = v
			}
		}
	}
	return tx
}

// Get returns the string under key with {name} filled in.
func (t Texts) Get(key, name string) string {
	return strings.ReplaceAll(t[key], "{name}", name)
}

var locale = regexp.MustCompile(`(?m)^locale="?([a-z]{2})`)

// Language returns the two-letter language Unraid's display settings in
// dynamix.cfg name, or "en".
func Language(cfg string) string {
	b, err := os.ReadFile(cfg)
	if err != nil {
		return "en"
	}
	if m := locale.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return "en"
}

// Sender is where a Watcher sends its events.
type Sender interface {
	Enabled() bool
	Send(ctx context.Context, e notify.Event) []notify.Result
}

// resolver hears that what a key was about is over, as the hub does.
type resolver interface {
	Resolve(app, key string)
}

// Journal hears what a Watcher does, for TollRoll's own status.
type Journal interface {
	Checked(at time.Time)
	Told(e notify.Event, results []notify.Result)
}

// Watcher reads the apps every so often while a channel is on and sends what
// changed. Check reads the board in a language, Texts names the language to
// write in and its strings. Journal may be nil.
type Watcher struct {
	Every    time.Duration
	Check    func(ctx context.Context, lang string) []board.Seen
	Channels Sender
	Texts    func() (string, Texts)
	Journal  Journal

	tracker *Tracker
}

// Run checks until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	tick := time.NewTicker(w.Every)
	defer tick.Stop()
	for {
		w.step(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// step forgets everything while no channel is on, so switching one on later
// starts by learning instead of telling what happened in between.
func (w *Watcher) step(ctx context.Context) {
	if !w.Channels.Enabled() {
		w.tracker = nil
		return
	}
	if w.tracker == nil {
		w.tracker = &Tracker{}
	}
	lang, tx := w.Texts()
	events := w.tracker.Next(w.Check(ctx, lang), tx)
	if w.Journal != nil {
		w.Journal.Checked(time.Now())
	}
	if r, ok := w.Channels.(resolver); ok {
		for _, g := range w.tracker.Gone() {
			r.Resolve(g.App, g.Key)
		}
	}
	for _, e := range events {
		sctx, cancel := context.WithTimeout(ctx, sendFor)
		results := w.Channels.Send(sctx, e)
		cancel()
		for _, r := range results {
			if !r.OK() {
				slog.Warn("notification failed", "channel", r.Channel, "code", r.Code, "err", r.Err)
			}
		}
		if w.Journal != nil {
			w.Journal.Told(e, results)
		}
	}
}
