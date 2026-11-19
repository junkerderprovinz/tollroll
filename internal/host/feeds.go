package host

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/board"
)

// The keys of the rows whose lines come from logs of their own.
const (
	Containers = "containers"
	Plugins    = "plugins"
	Scripts    = "scripts"
)

const (
	// feedFirst is how much of a log the first read takes from its end, and
	// feedMost how far behind a read may fall before it skips to the end:
	// a chatty container writes megabytes nobody reads anyway.
	feedFirst = 16 << 10
	feedMost  = 256 << 10
	keepFeed  = 60
	// userScripts is where the User Scripts plugin keeps the log of each
	// script's last run, in memory.
	userScripts = "/tmp/user.scripts/tmpScripts"
)

// feed is one log file of a container, a plugin or a user script.
type feed struct {
	app    string
	name   string
	docker bool
	t      tail
	read   bool
}

// feedLog keeps the lines of one kind of feed, the routine ones apart.
type feedLog struct {
	lines, quiet []entry
}

var (
	tagError = regexp.MustCompile(`(?i:\blevel=(?:error|err|fatal|crit|critical)\b)|\b(?:ERROR|FATAL|CRITICAL)\b`)
	tagWarn  = regexp.MustCompile(`(?i:\blevel=warn(?:ing)?\b|\[warn(?:ing)?\]|^warn(?:ing)?\b)|\b(?:WARN|WARNING)\b`)
)

// levelOf sorts a line no rule knows by its words: a crash or a level the
// line names itself, then words that sound like trouble.
func levelOf(msg string) status.Severity {
	switch {
	case worse.MatchString(msg), tagError.MatchString(msg):
		return fault
	case tagWarn.MatchString(msg), bad.MatchString(msg):
		return warn
	}
	return info
}

// readFeeds finds the logs to follow and reads on in each. The containers'
// logs are read only while the disk Docker lives on is awake.
func (h *Host) readFeeds(now time.Time, slow bool) {
	if h.feeds == nil {
		h.feeds, h.feedLogs = map[string]*feed{}, map[string]*feedLog{}
	}
	keep := map[string]bool{}
	if d := h.dockerDisk(); d == "" || !h.asleep(d) {
		for _, b := range h.boxes {
			if b.running && b.log != "" {
				h.follow(keep, h.path(b.log), Containers, b.name, true)
			}
		}
	}
	logs, _ := filepath.Glob(h.path(userScripts + "/*/log.txt"))
	for _, f := range logs {
		h.follow(keep, f, Scripts, filepath.Base(filepath.Dir(f)), false)
	}
	if slow {
		h.pluginLogs = h.findPluginLogs()
	}
	for f, name := range h.pluginLogs {
		h.follow(keep, f, Plugins, name, false)
	}
	for f := range h.feeds {
		if !keep[f] {
			delete(h.feeds, f)
		}
	}
	for _, f := range slices.Sorted(func(yield func(string) bool) {
		for f := range h.feeds {
			if !yield(f) {
				return
			}
		}
	}) {
		fd := h.feeds[f]
		var since time.Time
		if !fd.read {
			if fi, err := os.Stat(f); err == nil {
				since = fi.ModTime()
			}
		}
		fd.t.read(func(s string) { h.feedLine(fd, s, cmp.Or(since, now)) })
		fd.read = true
	}
}

func (h *Host) follow(keep map[string]bool, path, app, name string, docker bool) {
	keep[path] = true
	if _, ok := h.feeds[path]; ok {
		return
	}
	if app == Scripts {
		if b, err := os.ReadFile(h.path("/boot/config/plugins/user.scripts/scripts/" + name + "/name")); err == nil {
			name = cmp.Or(strings.TrimSpace(string(b)), name)
		}
	}
	h.feeds[path] = &feed{app: app, name: name, docker: docker, t: tail{path: path, first: feedFirst, most: feedMost}}
}

// feedLine takes one line of a feed. Docker writes each as JSON with its
// time, and a plugin's log often in the syslog's form; any other line counts
// as written when it was read.
func (h *Host) feedLine(fd *feed, s string, at time.Time) {
	msg := s
	if fd.docker {
		var l struct {
			Log  string    `json:"log"`
			Time time.Time `json:"time"`
		}
		if json.Unmarshal([]byte(s), &l) != nil {
			return
		}
		msg, at = l.Log, l.Time
	} else if l, ok := parse(s, at); ok {
		msg, at = l.raw(), l.At
	}
	msg = strings.TrimRightFunc(msg, unicode.IsSpace)
	if strings.TrimSpace(msg) == "" {
		return
	}
	level := levelOf(msg)
	h.feedAdd(entry{at: at, app: fd.app, level: level, quiet: level == info, raw: fd.name + ": " + msg})
}

func (h *Host) feedAdd(e entry) {
	if h.feedLogs == nil {
		h.feedLogs = map[string]*feedLog{}
	}
	h.seq++
	e.id = h.seq
	fl := h.feedLogs[e.app]
	if fl == nil {
		fl = &feedLog{}
		h.feedLogs[e.app] = fl
	}
	if e.quiet {
		fl.quiet = keep(append(fl.quiet, e), keepFeed)
	} else {
		fl.lines = keep(append(fl.lines, e), keepFeed)
	}
}

// squash reduces a name to its letters and digits, so a log file or a
// program tag matches its plugin however either is spelled.
func squash(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

// plugin names the installed plugin a name stands for.
func (h *Host) plugin(name string) (string, bool) {
	n := squash(name)
	for p := range h.watch.base.Plugins {
		if squash(p) == n {
			return p, true
		}
	}
	return "", false
}

// findPluginLogs finds the log files of the installed plugins in /var/log,
// which lives in memory: a file named after a plugin, or any file in a
// folder named after one.
func (h *Host) findPluginLogs() map[string]string {
	out := map[string]string{}
	entries, _ := os.ReadDir(h.path("/var/log"))
	for _, e := range entries {
		p := h.path("/var/log/" + e.Name())
		if e.IsDir() {
			name, ok := h.plugin(e.Name())
			if !ok {
				continue
			}
			files, _ := os.ReadDir(p)
			for _, f := range files {
				if f.Type().IsRegular() {
					out[filepath.Join(p, f.Name())] = name
				}
			}
			continue
		}
		base := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".log"), ".txt")
		if name, ok := h.plugin(base); ok && e.Type().IsRegular() {
			out[p] = name
		}
	}
	return out
}

// dockerDisk names the disk or pool Docker keeps its containers on, from
// what is mounted at /var/lib/docker: its image file's disk, or the disk
// whose file system holds its folder. Empty means it is not known.
func (h *Host) dockerDisk() string {
	b, _ := os.ReadFile(h.path("/proc/self/mountinfo"))
	var dev string
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) > 4 && f[4] == "/var/lib/docker" {
			dev = f[2]
		}
	}
	if dev == "" {
		return ""
	}
	if major, minor, _ := strings.Cut(dev, ":"); major == "7" {
		back, _ := os.ReadFile(h.path("/sys/block/loop" + minor + "/loop/backing_file"))
		rest, ok := strings.CutPrefix(strings.TrimSpace(string(back)), "/mnt/")
		disk, _, _ := strings.Cut(rest, "/")
		if !ok || disk == "user" || disk == "user0" {
			return ""
		}
		return disk
	}
	for mnt, d := range h.probe.mounts() {
		name, ok := strings.CutPrefix(mnt, "/mnt/")
		if ok && d == dev && !strings.Contains(name, "/") && !excluded[mnt] {
			return name
		}
	}
	return ""
}

// feedRows are the rows of the kinds of feed that have lines.
func (h *Host) feedRows(tx map[string]string) []board.App {
	var rows []board.App
	for _, k := range []struct{ app, key, name string }{
		{Containers, "h_containers", "Containers"}, {Plugins, "h_plugins", "Plugins"}, {Scripts, "h_scripts", "User Scripts"},
	} {
		if fl := h.feedLogs[k.app]; fl != nil && len(fl.lines)+len(fl.quiet) > 0 {
			rows = append(rows, board.App{Key: k.app, Name: cmp.Or(tx[k.key], k.name), State: string(status.StateOK), Instances: 1, Local: true})
		}
	}
	return rows
}
