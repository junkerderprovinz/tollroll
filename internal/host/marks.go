package host

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/junkerderprovinz/parleyport/kit/notify"
)

// What the user can say of a finding of the watch: that it is normal, or
// that it was a real incident.
const (
	MarkKnown    = "known"
	MarkIncident = "incident"
)

// ErrNoEntry is a mark for a finding that is not listed, or a mark that is
// neither of the two.
var ErrNoEntry = errors.New("host: no such finding")

const (
	// incidentFor is how long a real incident stays on record, and the same
	// from the same source is told at once.
	incidentFor = 30 * 24 * time.Hour
	maxNote     = 300
	keepKnown   = 500
	keepInc     = 100
	// incidentKind is the kind of the notification that an incident
	// happened again.
	incidentKind = "host.incident"
)

// incident is a finding the user kept as real, with what they noted.
type incident struct {
	App  string            `json:"app"`
	Key  string            `json:"key"`
	Subs []string          `json:"subs"`
	Args map[string]string `json:"args"`
	Note string            `json:"note,omitempty"`
	At   time.Time         `json:"at"`
}

func (i incident) id() string {
	sum := sha256.Sum256([]byte(i.App + "|" + i.Key + "|" + strings.Join(i.Subs, "\n")))
	return "i:" + hex.EncodeToString(sum[:6])
}

// subjectOf names, by the key of a finding, the values that say what it is
// about. The same deviation is the same key about the same thing.
var subjectOf = map[string][]string{
	"h_w_new_web": {"addr"}, "h_w_new_ssh": {"addr"}, "h_w_fails": {"addr"},
	"h_w_plugin": {"name"}, "h_w_cron": {"task"}, "h_w_flash": {"files"}, "h_w_guest": {"share", "proto"},
	"h_w_deleted": {"disk"}, "h_w_priv": {"name", "right"}, "h_w_io_read": {"disk"}, "h_w_io_write": {"disk"},
	"h_w_snap": {"fs"}, "h_w_at": {"job"},
	"h_r_new_login": {"router", "addr"}, "h_r_fails": {"router", "addr"}, "h_r_new_device": {"router", "mac"},
	"h_r_silent": {"router"}, "h_r_nat": {"router", "addr"}, "h_r_filter": {"router", "addr"},
	"h_r_config": {"router", "addr"}, "h_r_ids": {"router", "sig"},
}

// subjects lists what a finding is about: one thing for most, each file for
// the flash drive. None means it is no finding of the watch.
func subjects(key string, args map[string]string) []string {
	names, ok := subjectOf[key]
	if !ok {
		return nil
	}
	if key == "h_w_flash" {
		return strings.Split(args["files"], ", ")
	}
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, args[n])
	}
	return []string{strings.Join(parts, " ")}
}

func (w *watch) known(key, subject string) bool {
	_, ok := w.base.Known[key+"|"+subject]
	return ok
}

func (w *watch) accept(key string, subs []string, at time.Time) {
	if w.base.Known == nil {
		w.base.Known = map[string]time.Time{}
	}
	for _, s := range subs {
		w.base.Known[key+"|"+s] = at
	}
	trim(w.base.Known, keepKnown)
	w.changed = true
}

// incident returns the incident on record that a finding of key about
// subject on app repeats.
func (w *watch) incident(app, key, subject string) *incident {
	for i, inc := range w.base.Incidents {
		if inc.App == app && inc.Key == key && slices.Contains(inc.Subs, subject) {
			return &w.base.Incidents[i]
		}
	}
	return nil
}

// keep puts inc on record, in place of one about the same.
func (w *watch) keep(inc incident) {
	w.base.Incidents = slices.DeleteFunc(w.base.Incidents, func(o incident) bool { return o.id() == inc.id() })
	w.base.Incidents = append(w.base.Incidents, inc)
	if len(w.base.Incidents) > keepInc {
		w.base.Incidents = slices.Clone(w.base.Incidents[len(w.base.Incidents)-keepInc:])
	}
	w.changed = true
}

// unlearn forgets the address or device a finding of key learned, so the
// next time it shows up counts as new again.
func (w *watch) unlearn(key string, args map[string]string) {
	switch key {
	case "h_w_new_web", "h_w_new_ssh":
		delete(w.base.Addrs, args["addr"])
	case "h_r_new_login":
		delete(w.base.Admins, args["addr"])
	case "h_r_new_device":
		delete(w.base.Devices, args["mac"])
	}
}

// expire drops the incidents past their time.
func (w *watch) expire(now time.Time) {
	n := len(w.base.Incidents)
	w.base.Incidents = slices.DeleteFunc(w.base.Incidents, func(i incident) bool { return now.Sub(i.At) >= incidentFor })
	if len(w.base.Incidents) != n {
		w.changed = true
	}
}

// watched lists e on its row, unless the user took everything it is about
// as known: then it is a routine line. When it repeats a real incident, it
// is told at once.
func (h *Host) watched(e entry) {
	subs := subjects(e.key, e.args)
	if len(subs) > 0 && !slices.ContainsFunc(subs, func(s string) bool { return !h.watch.known(e.key, s) }) {
		e.quiet = true
		h.add(e)
		return
	}
	e.subs = subs
	e = h.add(e)
	h.need(e)
	for _, s := range subs {
		if inc := h.watch.incident(e.app, e.key, s); inc != nil {
			h.alarm(e, *inc)
			return
		}
	}
}

func (h *Host) alarm(e entry, inc incident) {
	tx := h.o.Texts(h.o.Lang())
	body := say(tx, e)
	if inc.Note != "" {
		body += "\n" + strings.ReplaceAll(tx["h_inc_note"], "{note}", inc.Note)
	}
	h.alarms = append(h.alarms, notify.Event{Kind: incidentKind, Severity: notify.SeverityCritical, Title: tx["h_inc_title"], Body: body,
		Fields: map[string]string{"key": incidentKind + ":" + e.app + ":" + strconv.FormatInt(e.id, 10)}})
}

func needID(e entry) string { return e.app + ":" + strconv.FormatInt(e.id, 10) }

// Mark takes the finding id as known or keeps it as a real incident with the
// user's note. An incident taken as known ends.
func (h *Host) Mark(id, as, note string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if (as != MarkKnown && as != MarkIncident) || h.watch == nil {
		return ErrNoEntry
	}
	w, now := h.watch, h.o.Now()
	if i := slices.IndexFunc(w.base.Incidents, func(inc incident) bool { return inc.id() == id }); i >= 0 {
		inc := w.base.Incidents[i]
		if as == MarkIncident {
			w.base.Incidents[i].Note = cleanNote(note)
		} else {
			w.base.Incidents = slices.Delete(w.base.Incidents, i, i+1)
			w.accept(inc.Key, inc.Subs, now)
		}
	} else {
		i := slices.IndexFunc(h.needs, func(e entry) bool { return len(e.subs) > 0 && needID(e) == id })
		if i < 0 {
			return ErrNoEntry
		}
		e := h.needs[i]
		h.needs = slices.Delete(h.needs, i, i+1)
		if as == MarkKnown {
			w.accept(e.key, e.subs, now)
			h.needs = slices.DeleteFunc(h.needs, func(o entry) bool {
				return o.key == e.key && len(o.subs) > 0 && !slices.ContainsFunc(o.subs, func(s string) bool { return !w.known(o.key, s) })
			})
		} else {
			w.keep(incident{App: e.app, Key: e.key, Subs: e.subs, Args: e.args, Note: cleanNote(note), At: now})
			w.unlearn(e.key, e.args)
		}
	}
	w.changed, h.dirty = false, true
	h.save()
	return nil
}

// cleanNote makes what the user noted one short line.
func cleanNote(s string) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if r := []rune(s); len(r) > maxNote {
		s = string(r[:maxNote])
	}
	return s
}
