// Package hub decides when a notification goes out: at once, held through
// the quiet hours, left to the daily summary, or not again. Everything
// TollRoll tells passes through it, its own messages about the apps and the
// ones the apps of the fortress hand to it.
package hub

import (
	"cmp"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/notify"
)

const (
	// sameFor is how long the same message stays told unless it is resolved
	// first.
	sameFor = 20 * time.Hour
	// perApp and perAll bound what goes out within burstWindow, from one app
	// and from all of them. What is past them waits for the next free slot.
	perApp      = 6
	perAll      = 20
	burstWindow = time.Hour
	// listed is how many held messages one collected message names.
	listed = 10
	// keepHeld and keepDay bound what the hub keeps of an app gone wrong
	// that raises thousands of messages, on the flash drive as well.
	keepHeld = 200
	keepDay  = 1000
	// dayFor is how far back the daily summary looks.
	dayFor = 24 * time.Hour
	// sendFor bounds the delivery of one message, retries included.
	sendFor = 2 * time.Minute
)

// Message is one thing to tell. App is the ID of the app it is about and
// Name how it is shown. Key names what it is about, so a message about the
// same thing is told once until a message with Resolved or a success says it
// is over. Without a Key, the words of the message are its key. Unraid sends
// it as one of Unraid's own notifications while no channel is on, for what
// the server must say either way.
type Message struct {
	App      string
	Name     string
	Key      string
	Resolved bool
	Unraid   bool
	Event    notify.Event
}

// Sender delivers to the channels.
type Sender interface {
	Enabled() bool
	Send(ctx context.Context, e notify.Event) []notify.Result
}

// Journal hears what the hub sent and how each channel took it.
type Journal interface {
	Told(e notify.Event, results []notify.Result)
}

// Options set up a Hub. Texts names the language to write in and its
// strings. Open lists what is still open across the apps, one line each, for
// the daily summary; nil leaves that out. State is where the held messages
// and what was told are kept over a restart. Unraid sends a message as an
// Unraid notification whatever channels are on.
type Options struct {
	Channels Sender
	Unraid   func(ctx context.Context, e notify.Event) notify.Result
	Settings *Store
	Texts    func() (string, map[string]string)
	Open     func(lang string) []string
	Journal  Journal
	State    string
	Now      func() time.Time
}

// Hub is safe for concurrent use.
type Hub struct {
	o Options

	mu      sync.Mutex
	told    map[string]time.Time
	sent    []stamp
	held    []kept
	day     []kept
	digest  string
	changed bool
}

type stamp struct {
	App string    `json:"app"`
	At  time.Time `json:"at"`
}

// kept is a message the hub keeps for later or for the summary.
type kept struct {
	App   string          `json:"app"`
	Name  string          `json:"name"`
	At    time.Time       `json:"at"`
	Title string          `json:"title"`
	Sev   notify.Severity `json:"severity"`
}

type state struct {
	Told   map[string]time.Time `json:"told"`
	Sent   []stamp              `json:"sent"`
	Held   []kept               `json:"held"`
	Day    []kept               `json:"day"`
	Digest string               `json:"digest"`
}

// New returns a hub with what it kept before its last stop.
func New(o Options) *Hub {
	if o.Now == nil {
		o.Now = time.Now
	}
	h := &Hub{o: o, told: map[string]time.Time{}}
	var s state
	if b, err := os.ReadFile(o.State); err == nil && json.Unmarshal(b, &s) == nil {
		h.sent, h.held, h.day, h.digest = s.Sent, s.Held, s.Day, s.Digest
		if s.Told != nil {
			h.told = s.Told
		}
	}
	return h
}

// Settings returns the store the hub reads its settings from.
func (h *Hub) Settings() *Store { return h.o.Settings }

// Enabled reports whether any channel is on.
func (h *Hub) Enabled() bool { return h.o.Channels.Enabled() }

// TakesOver reports whether the hub takes the notifications of the apps of
// the fortress: when it is told to and has a channel to send them to.
func (h *Hub) TakesOver() bool { return h.o.Settings.Get().TakeOver && h.Enabled() }

// Send offers TollRoll's own event about an app, as the alert watcher makes
// them, and returns how the channels took it when it went out at once. The
// app is named in its fields.
func (h *Hub) Send(ctx context.Context, e notify.Event) []notify.Result {
	return h.Offer(ctx, Message{App: e.Fields["appId"], Name: e.Fields["app"], Key: e.Fields["key"], Event: e})
}

// Resolve says that what key was about is over, so it may be told again.
func (h *Hub) Resolve(app, key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.told, app+"|"+key)
	h.changed = true
}

// Offer decides about m and sends it if it goes out now. A success ends
// what its key was about and is told like any message.
func (h *Hub) Offer(ctx context.Context, m Message) []notify.Result {
	now := h.o.Now()
	e := m.Event
	e.Fields = map[string]string{"app": m.Name}
	key := m.App + "|" + cmp.Or(m.Key, e.Kind+"|"+e.Title+"|"+e.Body)
	s := h.o.Settings.Get()

	h.mu.Lock()
	h.changed = true
	if m.Resolved || e.Severity == notify.SeveritySuccess {
		delete(h.told, key)
	}
	if m.Resolved {
		h.mu.Unlock()
		return nil
	}
	if at, ok := h.told[key]; ok && now.Sub(at) < sameFor && e.Severity != notify.SeveritySuccess {
		h.mu.Unlock()
		return nil
	}
	if e.Severity != notify.SeveritySuccess {
		h.told[key] = now
	}
	k := kept{App: m.App, Name: m.Name, At: now, Title: e.Title, Sev: e.Severity}
	h.day = last(append(h.day, k), keepDay)
	alert := e.Severity == notify.SeverityCritical
	switch {
	case alert:
	case levels[severityLevel(e.Severity)] < levels[cmp.Or(s.Apps[m.App], LevelInfo)]:
		h.mu.Unlock()
		return nil
	case s.Quiet.quiet(now) || !h.room(m.App, now):
		h.held = last(append(h.held, k), keepHeld)
		h.mu.Unlock()
		return nil
	}
	h.sent = append(h.sent, stamp{App: m.App, At: now})
	h.mu.Unlock()
	if m.Unraid && !h.Enabled() && h.o.Unraid != nil {
		sctx, cancel := context.WithTimeout(ctx, sendFor)
		defer cancel()
		return []notify.Result{h.o.Unraid(sctx, e)}
	}
	return h.deliver(ctx, e)
}

// severityLevel is the level an event of sev counts as. Good news counts as
// information.
func severityLevel(sev notify.Severity) string {
	switch sev {
	case notify.SeverityWarning:
		return LevelWarning
	case notify.SeverityFailure:
		return LevelFailure
	case notify.SeverityCritical:
		return LevelCritical
	}
	return LevelInfo
}

// room reports whether app may send one more message now. Called with mu
// held.
func (h *Hub) room(app string, now time.Time) bool {
	h.sent = slices.DeleteFunc(h.sent, func(s stamp) bool { return now.Sub(s.At) >= burstWindow })
	n := 0
	for _, s := range h.sent {
		if s.App == app {
			n++
		}
	}
	return n < perApp && len(h.sent) < perAll
}

func (h *Hub) deliver(ctx context.Context, e notify.Event) []notify.Result {
	sctx, cancel := context.WithTimeout(ctx, sendFor)
	defer cancel()
	results := h.o.Channels.Send(sctx, e)
	for _, r := range results {
		if !r.OK() {
			slog.Warn("notification failed", "channel", r.Channel, "code", r.Code, "err", r.Err)
		}
	}
	return results
}

// Deliver sends a message an app handed over and tells the journal, since
// no watcher waits for it.
func (h *Hub) Deliver(ctx context.Context, m Message) {
	e := m.Event
	if r := h.Offer(ctx, m); r != nil && h.o.Journal != nil {
		h.o.Journal.Told(e, r)
	}
}

// Run sends what waited and the daily summary when their time comes, once a
// minute until ctx ends, and keeps what it holds for the next start.
func (h *Hub) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		h.Tick(ctx)
		select {
		case <-ctx.Done():
			h.Save()
			return
		case <-tick.C:
		}
	}
}

// Tick sends the held messages as one, once the quiet hours are over and
// there is room, and the daily summary once its time has come today.
func (h *Hub) Tick(ctx context.Context) {
	now := h.o.Now()
	s := h.o.Settings.Get()
	if !h.Enabled() {
		return
	}
	lang, tx := h.o.Texts()
	var out []notify.Event
	h.mu.Lock()
	h.day = slices.DeleteFunc(h.day, func(k kept) bool { return now.Sub(k.At) >= dayFor })
	for key, at := range h.told {
		if now.Sub(at) >= sameFor {
			delete(h.told, key)
		}
	}
	quiet := s.Quiet.quiet(now)
	today := now.Format(time.DateOnly)
	due := s.Digest.On && !quiet && h.digest != today && now.Hour()*60+now.Minute() >= minutes(s.Digest.At)
	if due {
		h.digest = today
		out = append(out, h.summary(tx, lang))
		h.held = nil
		h.changed = true
	} else if len(h.held) > 0 && !quiet && h.room("", now) {
		out = append(out, collected(tx, h.held))
		h.sent = append(h.sent, stamp{At: now})
		h.held = nil
		h.changed = true
	}
	h.mu.Unlock()
	for _, e := range out {
		r := h.deliver(ctx, e)
		if h.o.Journal != nil {
			h.o.Journal.Told(e, r)
		}
	}
}

// collected is one message for the messages that waited, the latest last.
func collected(tx map[string]string, held []kept) notify.Event {
	var lines []string
	for _, k := range held[max(0, len(held)-listed):] {
		lines = append(lines, k.Name+": "+k.Title)
	}
	if n := len(held) - listed; n > 0 {
		lines = append(lines, strings.ReplaceAll(tx["n_held_more"], "{n}", strconv.Itoa(n)))
	}
	return notify.Event{Kind: "tollroll.held", Severity: worst(held), Title: tx["n_held_title"], Body: strings.Join(lines, "\n")}
}

// summary is the daily message: what each app said in the last day, most
// first, and what is still open. Held messages are in it already.
func (h *Hub) summary(tx map[string]string, lang string) notify.Event {
	type app struct {
		name, last string
		n          int
	}
	var apps []*app
	by := map[string]*app{}
	for _, k := range h.day {
		a := by[k.App]
		if a == nil {
			a = &app{name: k.Name}
			by[k.App] = a
			apps = append(apps, a)
		}
		a.n++
		a.last = k.Title
	}
	slices.SortStableFunc(apps, func(a, b *app) int { return b.n - a.n })
	var lines []string
	for _, a := range apps {
		lines = append(lines, strings.NewReplacer("{name}", a.name, "{n}", strconv.Itoa(a.n), "{title}", a.last).Replace(tx["n_digest_line"]))
	}
	if len(lines) == 0 {
		lines = append(lines, tx["n_digest_none"])
	}
	if h.o.Open != nil {
		if open := h.o.Open(lang); len(open) > 0 {
			lines = append(lines, "", strings.ReplaceAll(tx["n_digest_open"], "{n}", strconv.Itoa(len(open))))
			lines = append(lines, open[:min(len(open), listed)]...)
		}
	}
	return notify.Event{Kind: "tollroll.digest", Severity: notify.SeverityInfo, Title: tx["n_digest_title"], Body: strings.Join(lines, "\n")}
}

// last keeps the newest n of ks.
func last(ks []kept, n int) []kept {
	if len(ks) > n {
		return slices.Clone(ks[len(ks)-n:])
	}
	return ks
}

func worst(ks []kept) notify.Severity {
	out := notify.SeverityInfo
	for _, k := range ks {
		if levels[severityLevel(k.Sev)] > levels[severityLevel(out)] {
			out = k.Sev
		}
	}
	return out
}

// Save keeps what the hub holds, when anything changed since the last save.
func (h *Hub) Save() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.changed || h.o.State == "" {
		return
	}
	b, err := json.Marshal(state{Told: h.told, Sent: h.sent, Held: h.held, Day: h.day, Digest: h.digest})
	if err == nil {
		err = os.MkdirAll(filepath.Dir(h.o.State), 0o700)
	}
	if err == nil {
		tmp := h.o.State + ".tmp"
		if err = os.WriteFile(tmp, b, 0o600); err == nil {
			err = os.Rename(tmp, h.o.State)
		}
	}
	if err != nil {
		slog.Warn("could not save the held notifications", "err", err)
		return
	}
	h.changed = false
}
