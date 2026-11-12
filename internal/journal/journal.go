// Package journal is what TollRoll tells the other apps of its fortress about
// itself through the status contract: whether a notification channel fails,
// when it checks the apps next and a log of what it told you.
package journal

import (
	"cmp"
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/notify"
	"github.com/junkerderprovinz/parleyport/kit/status"
)

// maxLog is how many lines the journal keeps. It lives in memory and starts
// empty with every start of the service.
const maxLog = 300

// Journal answers the status contract for TollRoll. It is safe for
// concurrent use.
type Journal struct {
	App status.App
	// Every is how often the watcher checks the apps while a channel is on.
	Every time.Duration
	// Texts returns the plugin's strings in lang, English where a language
	// lacks one.
	Texts func(lang string) map[string]string
	Now   func() time.Time

	mu       sync.Mutex
	instance status.Instance
	checked  time.Time
	failing  map[string]string
	log      []entry
	nextID   int64
}

// entry keeps a line as a key and its values, so it reads in the language
// each reader asks for.
type entry struct {
	id    int64
	at    time.Time
	level status.Severity
	key   string
	args  map[string]string
}

func (j *Journal) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

// SetInstance names the member the journal reports for.
func (j *Journal) SetInstance(in status.Instance) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.instance = in
}

// Checked notes that the watcher read the apps at at.
func (j *Journal) Checked(at time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.checked = at
}

var told = map[string]struct {
	key   string
	level status.Severity
}{
	"app.unreachable": {"r_told_down", status.SeverityWarn},
	"app.reachable":   {"r_told_back", status.SeverityInfo},
	"app.needs_you":   {"r_told_need", status.SeverityWarn},
}

// Told logs a notification the watcher sent and how each channel took it. A
// channel that failed needs you until it takes one again.
func (j *Journal) Told(e notify.Event, results []notify.Result) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.failing == nil {
		j.failing = map[string]string{}
	}
	took := false
	for _, r := range results {
		if r.OK() {
			took = true
			delete(j.failing, r.Channel)
			continue
		}
		j.failing[r.Channel] = string(r.Kind)
		j.add(status.SeverityError, "r_send_failed", map[string]string{"channel": string(r.Kind)})
	}
	if t, ok := told[e.Kind]; ok && took {
		j.add(t.level, t.key, map[string]string{"name": e.Fields["app"]})
	}
}

// Removed logs a member TollRoll took out of the fortress.
func (j *Journal) Removed(app, device string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.add(status.SeverityInfo, "r_removed", map[string]string{"app": app, "device": device})
}

// add appends to the log. Called with mu held.
func (j *Journal) add(level status.Severity, key string, args map[string]string) {
	at := j.now()
	// Ids start at the clock, so a cursor from before a restart sits below
	// every new line.
	if j.nextID == 0 {
		j.nextID = at.UnixNano()
	}
	j.nextID++
	j.log = append(j.log, entry{id: j.nextID, at: at, level: level, key: key, args: args})
	if len(j.log) > maxLog {
		j.log = append(j.log[:0:0], j.log[len(j.log)-maxLog:]...)
	}
}

// Report answers the status contract's report.
func (j *Journal) Report(_ context.Context, lang string) (status.Report, error) {
	tx := j.texts(lang)
	j.mu.Lock()
	defer j.mu.Unlock()
	r := status.Report{App: j.App, Instance: j.instance, State: status.StateOK}
	for channel, kind := range j.failing {
		r.NeedsYou = append(r.NeedsYou, status.Need{ID: "send-" + channel, Severity: status.SeverityWarn, Code: "send_failed",
			Text: say(tx, "r_send_failed", map[string]string{"channel": kind})})
	}
	if len(r.NeedsYou) > 0 {
		r.State = status.StateWarning
	}
	if !j.checked.IsZero() && j.now().Sub(j.checked) < 2*j.Every {
		r.Next = []status.Scheduled{{Kind: status.KindCheck, Title: tx["r_next_check"], At: j.checked.Add(j.Every)}}
	}
	return r, nil
}

// Log answers the status contract's log, oldest line first.
func (j *Journal) Log(_ context.Context, q status.LogQuery) (status.LogPage, error) {
	after, _ := strconv.ParseInt(q.After, 10, 64)
	tx := j.texts(q.Lang)
	j.mu.Lock()
	defer j.mu.Unlock()
	page := status.LogPage{Next: q.After}
	for _, e := range j.log {
		if e.id <= after {
			continue
		}
		if len(page.Entries) == q.Limit {
			break
		}
		page.Entries = append(page.Entries, status.LogEntry{ID: strconv.FormatInt(e.id, 10), At: e.at, Level: e.level, Code: e.key, Text: say(tx, e.key, e.args)})
		page.Next = strconv.FormatInt(e.id, 10)
	}
	return page, nil
}

func (j *Journal) texts(lang string) map[string]string {
	primary, _, _ := strings.Cut(strings.ToLower(lang), "-")
	return j.Texts(primary)
}

// say fills the placeholders of key. A channel goes by the name the settings
// page gives it.
func say(tx map[string]string, key string, args map[string]string) string {
	s := tx[key]
	for k, v := range args {
		if k == "channel" {
			v = cmp.Or(tx["s_n_"+v], v)
		}
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}
