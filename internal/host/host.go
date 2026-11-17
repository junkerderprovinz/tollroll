// Package host watches the Unraid server TollRoll runs on and puts it on the
// tile: its syslog in plain words, the disks that wake up and why, unusual
// changes such as a sign-in from a new address, and how full /var/log is.
//
// Everything is read, nothing is changed, and nothing it reads touches a
// disk: the syslog and /var/log live in memory, the rest comes from /proc and
// the state files emhttpd keeps.
package host

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/notify"
	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/board"
)

const (
	// every is how often the syslog, the disks and the containers are read.
	every = 30 * time.Second
	// slowEvery is how often the files the watch compares are read, and the
	// room in /var/log.
	slowEvery = 5 * time.Minute
	// saveEvery bounds the writes to the flash drive. Nothing is saved while
	// nothing changed.
	saveEvery = 5 * time.Minute
	// needFor is how long an error or an unusual event stays on the row.
	needFor = 24 * time.Hour
	// sameWake is the time within which a disk cannot wake twice, since
	// Unraid spins none down sooner.
	sameWake   = 10 * time.Minute
	keepLines  = 150
	keepQuiet  = 80
	keepNeeds  = 50
	listedNeed = 5
)

// The keys of the rows.
const (
	Unraid = "unraid"
	Disks  = "disks"
)

// Space is what statfs says of a file system.
type Space struct {
	Total, Free, Files, FilesFree uint64
}

// Options set up a Host. Root is where the server's files are, "/" when
// empty. State is the file on the flash drive that keeps what the watch
// learned and the wake-up counts. Texts returns the plugin's strings in a
// language and Lang names Unraid's, for the notification. Notify sends the
// one notification the host sends, when /var/log is nearly full.
type Options struct {
	Root   string
	State  string
	Texts  func(lang string) map[string]string
	Lang   func() string
	Notify func(ctx context.Context, e notify.Event)
	Now    func() time.Time
	Space  func(path string) (Space, error)
}

// Host is safe for concurrent use.
type Host struct {
	o     Options
	probe probe

	mu       sync.Mutex
	started  bool
	unraid   bool
	version  string
	syslog   tail
	hadState bool
	through  time.Time
	watch    *watch
	spins    spins
	wakes    tally
	last     map[string]lastWake
	disks    []disk
	moving   bool
	unclean  bool
	arrayAt  time.Time
	room     room
	roomNeed *entry
	boxes    map[string]box
	vms      map[string]bool
	lines    []entry
	quiet    []entry
	needs    []entry
	seq      int64
	slowAt   time.Time
	savedAt  time.Time
	dirty    bool
}

// entry is a line of the log as a key and its values, so it reads in the
// language of each reader. raw is the syslog line it came from.
type entry struct {
	id    int64
	at    time.Time
	app   string
	level status.Severity
	quiet bool
	key   string
	args  map[string]string
	raw   string
}

type lastWake struct {
	At  time.Time `json:"at"`
	By  string    `json:"by"`
	Who string    `json:"who,omitempty"`
}

// box is a container as its config file last said.
type box struct {
	name    string
	running bool
	mod     time.Time
}

type saved struct {
	Through time.Time           `json:"through"`
	Watch   baseline            `json:"watch"`
	Wakes   tally               `json:"wakes"`
	Last    map[string]lastWake `json:"last"`
}

// New returns a host that reads nothing until its first step.
func New(o Options) *Host {
	o.Root = cmp.Or(o.Root, "/")
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Space == nil {
		o.Space = statfs
	}
	if o.Lang == nil {
		o.Lang = func() string { return "en" }
	}
	return &Host{o: o, probe: probe{root: o.Root}, syslog: tail{path: filepath.Join(o.Root, "/var/log/syslog")}}
}

// Run reads the server every 30 seconds until ctx ends, and then saves what
// it learned.
func (h *Host) Run(ctx context.Context) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		h.step(ctx)
		select {
		case <-ctx.Done():
			h.mu.Lock()
			h.save()
			h.mu.Unlock()
			return
		case <-tick.C:
		}
	}
}

func (h *Host) path(p string) string { return filepath.Join(h.o.Root, p) }

func (h *Host) step(ctx context.Context) {
	h.mu.Lock()
	tell := h.look()
	h.mu.Unlock()
	for _, e := range tell {
		if h.o.Notify != nil {
			sctx, cancel := context.WithTimeout(ctx, time.Minute)
			h.o.Notify(sctx, e)
			cancel()
		}
	}
}

// look reads what changed since the last one and returns the notifications
// to send. Called with mu held.
func (h *Host) look() []notify.Event {
	now := h.o.Now()
	if !h.started {
		h.start()
	}
	if !h.unraid {
		return nil
	}
	h.disks = readDisks(h.o.Root)
	h.syslog.read(func(s string) {
		if l, ok := parse(s, now); ok {
			h.ingest(l, now)
		}
	})
	for _, d := range h.spins.look(h.disks) {
		if h.started {
			h.woke(d, now, now)
		}
	}
	h.boxesAndVMs(now)
	var tell []notify.Event
	if !h.started || now.Sub(h.slowAt) >= slowEvery {
		h.slowAt = now
		tell = h.slow(now)
	}
	h.needs = slices.DeleteFunc(h.needs, func(e entry) bool { return now.Sub(e.at) >= needFor })
	h.wakes.prune(now)
	h.started = true
	if h.dirty && now.Sub(h.savedAt) >= saveEvery {
		h.save()
	}
	return tell
}

var unraidVersion = regexp.MustCompile(`version="([^"]+)"`)

func (h *Host) start() {
	b, err := os.ReadFile(h.path("/etc/unraid-version"))
	h.unraid = err == nil
	if m := unraidVersion.FindSubmatch(b); m != nil {
		h.version = string(m[1])
	}
	var s saved
	if b, err := os.ReadFile(h.o.State); err == nil && json.Unmarshal(b, &s) == nil {
		h.hadState = true
	}
	h.through = s.Through
	h.watch = newWatch(s.Watch)
	h.wakes = s.Wakes
	if h.wakes == nil {
		h.wakes = tally{}
	}
	h.last = s.Last
	if h.last == nil {
		h.last = map[string]lastWake{}
	}
	h.savedAt = h.o.Now()
}

func (h *Host) save() {
	if !h.dirty || h.o.State == "" {
		return
	}
	b, err := json.Marshal(saved{Through: h.through, Watch: h.watch.base, Wakes: h.wakes, Last: h.last})
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
		slog.Warn("could not save what the watch learned", "err", err)
		return
	}
	h.dirty, h.savedAt = false, h.o.Now()
}

// ingest takes one syslog line. A line is live once the host has looked
// before, or when it is newer than what the saved state had seen; older ones
// teach the watch without telling.
func (h *Host) ingest(l line, now time.Time) {
	live := h.started || (h.hadState && l.At.After(h.through))
	if l.At.After(h.through) {
		h.through = l.At
	}
	c := classify(l)
	if dev, ok := strings.CutPrefix(l.Msg, "read SMART /dev/"); ok && l.Prog == "emhttpd" {
		if h.spins.up(dev) && live {
			if d, ok := h.byDevice(dev); ok {
				h.woke(d, l.At, now)
			}
		}
	}
	switch c.Key {
	case "h_s_spun_down":
		h.spins.down(c.Args["dev"])
	case "h_s_mover_start":
		h.moving = true
	case "h_s_mover_done":
		h.moving = false
	case "h_s_unclean":
		h.unclean = true
	case "h_s_array_start":
		h.arrayAt = l.At
	case "h_s_array_stop":
		h.unclean = false
	case "h_s_parity_start":
		c.Key = h.parityCause(l.At)
	}
	if dev := c.Args["dev"]; dev != "" {
		if d, ok := h.byDevice(dev); ok {
			c.Args["dev"] = diskLabel(d.Name) + " (" + dev + ")"
		}
	}
	if addr := c.Args["addr"]; addr != "" {
		known := h.watch.knows(addr)
		for _, f := range h.watch.signIn(c, l.At, live) {
			h.found(f, l.At)
		}
		if (c.Key == "h_s_web_ok" || c.Key == "h_s_ssh_ok") && (known || !live) {
			c.Quiet = true
		}
	}
	e := entry{at: l.At, app: Unraid, level: c.Level, quiet: c.Quiet, key: c.Key, args: c.Args, raw: l.raw()}
	h.add(e)
	if c.Level == fault && c.Key != "" && now.Sub(l.At) < needFor {
		h.need(e)
	}
	if h.watch.changed {
		h.watch.changed, h.dirty = false, true
	}
}

func (h *Host) byDevice(dev string) (disk, bool) {
	for _, d := range h.disks {
		if d.Device == dev {
			return d, true
		}
	}
	return disk{}, false
}

// add appends e to the log, the routine lines apart so they cannot push the
// others out.
func (h *Host) add(e entry) {
	h.seq++
	e.id = h.seq
	if e.quiet {
		h.quiet = keep(append(h.quiet, e), keepQuiet)
	} else {
		h.lines = keep(append(h.lines, e), keepLines)
	}
}

func keep(l []entry, n int) []entry {
	if len(l) > n {
		return slices.Clone(l[len(l)-n:])
	}
	return l
}

// need puts e on the row, in place of an older one that says the same.
func (h *Host) need(e entry) {
	h.needs = slices.DeleteFunc(h.needs, func(o entry) bool { return o.key == e.key && fmt.Sprint(o.args) == fmt.Sprint(e.args) })
	h.needs = keep(append(h.needs, e), keepNeeds)
}

func (h *Host) found(f finding, at time.Time) {
	e := entry{at: at, app: Unraid, level: f.Level, key: f.Key, args: f.Args}
	h.add(e)
	h.need(e)
}

// woke counts a wake of d at at. What has files open now says nothing about
// a wake long gone, such as one from before the service ran, whose cause
// stays unknown.
func (h *Host) woke(d disk, at, now time.Time) {
	if l, ok := h.last[d.Name]; ok && at.Sub(l.At) < sameWake {
		return
	}
	c := unknown
	if now.Sub(at) < sameWake {
		c = h.probe.cause(d)
	}
	h.wakes.add(at, d.Name)
	h.last[d.Name] = lastWake{At: at, By: c.By, Who: c.Who}
	h.dirty = true
	h.add(entry{at: at, app: Disks, level: info, key: "h_woke", args: map[string]string{"disk": diskLabel(d.Name), "by": c.By, "who": c.Who}})
}

// boxesAndVMs logs the containers and VMs that started or stopped since the
// last look. A container's config file changes with its state, so only the
// changed ones are read again. A container that Unraid's autostart reported
// a moment ago is not told twice.
func (h *Host) boxesAndVMs(now time.Time) {
	files, _ := filepath.Glob(h.path("/var/lib/docker/containers/*/config.v2.json"))
	next := map[string]box{}
	for _, f := range files {
		id := filepath.Base(filepath.Dir(f))
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		old, seen := h.boxes[id]
		if seen && fi.ModTime().Equal(old.mod) {
			next[id] = old
			continue
		}
		var c struct {
			Name  string
			State struct{ Running bool }
		}
		b, err := os.ReadFile(f)
		if err != nil || json.Unmarshal(b, &c) != nil {
			continue
		}
		bx := box{name: strings.TrimPrefix(c.Name, "/"), running: c.State.Running, mod: fi.ModTime()}
		next[id] = bx
		if h.boxes != nil && seen && bx.running != old.running {
			key := "h_s_container_down"
			if bx.running {
				key = "h_s_container_up"
			}
			if !h.toldLately(key, bx.name, now) {
				h.add(entry{at: now, app: Unraid, level: info, key: key, args: map[string]string{"name": bx.name}})
			}
		}
	}
	h.boxes = next

	pids, _ := filepath.Glob(h.path("/var/run/libvirt/qemu/*.pid"))
	vms := map[string]bool{}
	for _, p := range pids {
		name := strings.TrimSuffix(filepath.Base(p), ".pid")
		if _, err := os.Stat(strings.TrimSuffix(p, ".pid") + ".xml"); err == nil {
			vms[name] = true
		}
	}
	if h.vms != nil {
		for _, name := range union(vms, h.vms) {
			if vms[name] != h.vms[name] {
				key := "h_s_vm_down"
				if vms[name] {
					key = "h_s_vm_up"
				}
				h.add(entry{at: now, app: Unraid, level: info, key: key, args: map[string]string{"name": name}})
			}
		}
	}
	h.vms = vms
}

func union(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		out = append(out, k)
	}
	for k := range b {
		if !a[k] {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func (h *Host) toldLately(key, name string, now time.Time) bool {
	for _, e := range slices.Backward(h.lines) {
		if now.Sub(e.at) > 2*time.Minute {
			return false
		}
		if e.key == key && e.args["name"] == name {
			return true
		}
	}
	return false
}

// excluded are mounts under /mnt that are no disk of this server.
var excluded = map[string]bool{"/mnt/user": true, "/mnt/user0": true, "/mnt/disks": true, "/mnt/remotes": true, "/mnt/rootshare": true, "/mnt/addons": true}

// slow runs the watch over the files it compares and checks /var/log.
func (h *Host) slow(now time.Time) []notify.Event {
	w := h.watch
	var fs []finding
	fs = append(fs, w.plugins(readPlugins(h.o.Root))...)
	fs = append(fs, w.cron(readCron(h.o.Root))...)
	fs = append(fs, w.flash(readFlash(h.o.Root))...)
	fs = append(fs, w.guests(readGuests(h.o.Root))...)
	used := map[string]uint64{}
	for mnt := range h.probe.mounts() {
		name, ok := strings.CutPrefix(mnt, "/mnt/")
		if !ok || strings.Contains(name, "/") || excluded[mnt] {
			continue
		}
		if sp, err := h.o.Space(h.path(mnt)); err == nil && sp.Files > 0 {
			used[name] = sp.Files - sp.FilesFree
		}
	}
	fs = append(fs, w.deletes(used, h.moving || h.probe.moverRunning())...)
	for _, f := range fs {
		h.found(f, now)
	}
	if w.changed {
		w.changed, h.dirty = false, true
	}
	return h.varLog(now)
}

func (h *Host) varLog(now time.Time) []notify.Event {
	sp, err := h.o.Space(h.path("/var/log"))
	if err != nil || sp.Total == 0 {
		return nil
	}
	pct := int((sp.Total - sp.Free) * 100 / sp.Total)
	note, tell, back := h.room.step(pct)
	if back {
		h.roomNeed = nil
		h.add(entry{at: now, app: Unraid, level: info, key: "h_room_ok", args: map[string]string{"pct": strconv.Itoa(pct)}})
	}
	if pct < 80 {
		return nil
	}
	file, size := biggest(h.o.Root, "/var/log")
	c := h.probe.writer(file)
	e := entry{at: now, app: Unraid, level: warn, key: "h_room_who", args: map[string]string{"pct": strconv.Itoa(pct), "file": file, "size": human(size), "by": c.By, "who": c.Who}}
	if c.By == "unknown" {
		e.key = "h_room"
	}
	if pct >= 90 {
		e.level = fault
	}
	h.roomNeed = &e
	if note {
		h.add(e)
	}
	if !tell {
		return nil
	}
	tx := h.o.Texts(h.o.Lang())
	return []notify.Event{{
		Kind: "host.varlog_full", Severity: notify.SeverityWarning,
		Title: tx["h_room_title"], Body: say(tx, entry{key: "h_room_body", args: e.args}),
	}}
}

func human(n int64) string {
	units := []string{"B", "KB", "MB", "GB"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v, i = v/1024, i+1
	}
	if v < 10 && v != float64(int64(v)) {
		return fmt.Sprintf("%.1f %s", v, units[i])
	}
	return fmt.Sprintf("%.0f %s", v, units[i])
}

// say words e in tx. A cause is named by its phrase, which fills both
// {cause} and {who}. A line without a sentence keeps its own words.
func say(tx map[string]string, e entry) string {
	s, ok := tx[e.key]
	if !ok || e.key == "" {
		return e.raw
	}
	if by, ok := e.args["by"]; ok {
		phrase := strings.ReplaceAll(tx["h_by_"+by], "{name}", e.args["who"])
		s = strings.ReplaceAll(strings.ReplaceAll(s, "{cause}", phrase), "{who}", phrase)
	}
	for k, v := range e.args {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// Rows returns the row of the server and the row of its disks, none off
// Unraid.
func (h *Host) Rows(lang string) []board.App {
	tx := h.o.Texts(lang)
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.unraid {
		return nil
	}
	srv := board.App{Key: Unraid, Name: "Unraid", Version: h.version, State: string(status.StateOK), Instances: 1, Local: true, Color: "#f15a2c"}
	needs := slices.Clone(h.needs)
	if h.roomNeed != nil {
		needs = append(needs, *h.roomNeed)
	}
	slices.SortStableFunc(needs, func(a, b entry) int {
		return cmp.Or(severity[b.level]-severity[a.level], b.at.Compare(a.at))
	})
	for _, n := range needs[:min(len(needs), listedNeed)] {
		srv.Needs = append(srv.Needs, board.Need{Severity: string(n.level), Text: say(tx, n)})
	}
	if len(srv.Needs) > 0 {
		srv.State = string(status.StateWarning)
		srv.Need = &srv.Needs[0]
	}
	rows := []board.App{srv}
	if len(h.disks) == 0 {
		return rows
	}
	now := h.o.Now()
	ds := board.App{Key: Disks, Name: cmp.Or(tx["h_disks"], "Disks"), State: string(status.StateOK), Instances: 1, Local: true}
	total := 0
	for _, d := range h.disks {
		today, week := h.wakes.counts(d.Name, now)
		total += today
		row := board.Disk{Name: diskLabel(d.Name), Today: today, Week: week}
		if l, ok := h.last[d.Name]; ok {
			row.LastAt = l.At
			row.Last = strings.ReplaceAll(tx["h_by_"+l.By], "{name}", l.Who)
		}
		ds.Disks = append(ds.Disks, row)
	}
	ds.Note = tx["h_wakes_none"]
	if total > 0 {
		ds.Note = strings.ReplaceAll(tx["h_wakes_today"], "{n}", strconv.Itoa(total))
	}
	return append(rows, ds)
}

var severity = map[status.Severity]int{info: 0, warn: 1, fault: 2}

// Lines returns the server's lines for the shared log.
func (h *Host) Lines(lang string) []board.Line {
	tx := h.o.Texts(lang)
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]board.Line, 0, len(h.lines)+len(h.quiet))
	for _, l := range [][]entry{h.lines, h.quiet} {
		for _, e := range l {
			out = append(out, board.Line{ID: e.app + ":" + strconv.FormatInt(e.id, 10), At: e.at, App: e.app, Level: string(e.level), Text: say(tx, e), Raw: e.raw, Quiet: e.quiet})
		}
	}
	return out
}
