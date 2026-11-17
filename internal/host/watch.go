package host

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/status"
)

// finding is something unusual the watch saw, worded by its key.
type finding struct {
	Level status.Severity
	Key   string
	Args  map[string]string
}

// baseline is what the watch has learned to be normal. It is kept on the
// flash drive, so a restart does not report everything again. A nil field
// has never been looked at; its first look learns without a word.
type baseline struct {
	Addrs   map[string]time.Time `json:"addrs"`
	Plugins map[string]bool      `json:"plugins"`
	Cron    map[string]bool      `json:"cron"`
	Flash   map[string]string    `json:"flash"`
	Guests  map[string]bool      `json:"guests"`
	// Routers holds when each device that sends its log here sent its last
	// line, Admins the addresses its admins signed in from and Devices the
	// hardware addresses its DHCP server handed out to.
	Routers map[string]time.Time `json:"routers,omitempty"`
	Admins  map[string]time.Time `json:"admins,omitempty"`
	Devices map[string]time.Time `json:"devices,omitempty"`
}

const (
	// burst is how many failed sign-ins from one address within burstWindow
	// are worth telling.
	burst       = 5
	burstWindow = 10 * time.Minute
	burstQuiet  = time.Hour
	// massDelete is how many files gone from one disk between two looks
	// count as many deleted at once.
	massDelete = 1000
	// keepAddrs bounds the addresses kept, so a scan of the server from many
	// addresses cannot grow the file on the flash drive without end.
	keepAddrs = 500
)

type watch struct {
	base    baseline
	fails   map[string][]time.Time
	told    map[string]time.Time
	inodes  map[string]uint64
	changed bool
}

func newWatch(b baseline) *watch {
	return &watch{base: b, fails: map[string][]time.Time{}, told: map[string]time.Time{}}
}

// knows reports whether addr signed in before.
func (w *watch) knows(addr string) bool {
	_, ok := w.base.Addrs[addr]
	return ok
}

// signIn follows the sign-ins of the syslog. live is false for lines from
// before the watch started, which it learns from without telling.
func (w *watch) signIn(c class, at time.Time, live bool) []finding {
	addr := c.Args["addr"]
	switch c.Key {
	case "h_s_web_ok", "h_s_ssh_ok":
		if w.knows(addr) {
			return nil
		}
		if w.base.Addrs == nil {
			w.base.Addrs = map[string]time.Time{}
		}
		w.base.Addrs[addr] = at
		trim(w.base.Addrs, keepAddrs)
		w.changed = true
		if !live {
			return nil
		}
		key := "h_w_new_web"
		if c.Key == "h_s_ssh_ok" {
			key = "h_w_new_ssh"
		}
		return []finding{{Level: warn, Key: key, Args: map[string]string{"user": c.Args["user"], "addr": addr}}}
	case "h_s_web_fail", "h_s_ssh_fail":
		recent := slices.DeleteFunc(append(w.fails[addr], at), func(t time.Time) bool { return at.Sub(t) >= burstWindow })
		w.fails[addr] = recent
		if !live || len(recent) < burst || at.Sub(w.told[addr]) < burstQuiet {
			return nil
		}
		w.told[addr] = at
		return []finding{{Level: warn, Key: "h_w_fails", Args: map[string]string{"addr": addr, "n": strconv.Itoa(len(recent))}}}
	}
	return nil
}

// added learns now as the set at *known and returns what is new in it,
// nothing on the first look.
func (w *watch) added(known *map[string]bool, now []string) []string {
	next := map[string]bool{}
	var out []string
	for _, s := range now {
		next[s] = true
		if *known != nil && !(*known)[s] {
			out = append(out, s)
		}
	}
	if *known == nil || len(out) > 0 || len(next) != len(*known) {
		w.changed = true
	}
	*known = next
	slices.Sort(out)
	return out
}

func (w *watch) plugins(names []string) []finding {
	var out []finding
	for _, n := range w.added(&w.base.Plugins, names) {
		out = append(out, finding{Level: warn, Key: "h_w_plugin", Args: map[string]string{"name": n}})
	}
	return out
}

func (w *watch) cron(lines []string) []finding {
	var out []finding
	for _, l := range w.added(&w.base.Cron, lines) {
		out = append(out, finding{Level: warn, Key: "h_w_cron", Args: map[string]string{"task": l}})
	}
	return out
}

func (w *watch) guests(open []string) []finding {
	var out []finding
	for _, g := range w.added(&w.base.Guests, open) {
		share, proto, _ := strings.Cut(g, ":")
		out = append(out, finding{Level: warn, Key: "h_w_guest", Args: map[string]string{"share": share, "proto": proto}})
	}
	return out
}

// sensitive are the files of the flash config that decide who gets in and
// what runs at boot.
var sensitive = []string{"go", "passwd", "shadow", "smbpasswd", "ssh/"}

// flash compares the stamps of the flash config's files with the last look
// and tells which files changed, appeared or went.
func (w *watch) flash(stamps map[string]string) []finding {
	if w.base.Flash == nil {
		w.base.Flash, w.changed = stamps, true
		return nil
	}
	var files []string
	for f, s := range stamps {
		if w.base.Flash[f] != s {
			files = append(files, f)
		}
	}
	for f := range w.base.Flash {
		if _, ok := stamps[f]; !ok {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil
	}
	w.base.Flash, w.changed = stamps, true
	slices.Sort(files)
	level := info
	for _, f := range files {
		for _, s := range sensitive {
			if f == s || (strings.HasSuffix(s, "/") && strings.HasPrefix(f, s)) {
				level = warn
			}
		}
	}
	return []finding{{Level: level, Key: "h_w_flash", Args: map[string]string{"files": strings.Join(files, ", ")}}}
}

// deletes compares the files in use on each disk, by its inodes, with the
// last look. The mover empties the cache by design, so while it runs a drop
// says nothing.
func (w *watch) deletes(used map[string]uint64, moving bool) []finding {
	var out []finding
	for name, n := range used {
		if before, ok := w.inodes[name]; ok && !moving && before > n && before-n >= massDelete {
			out = append(out, finding{Level: warn, Key: "h_w_deleted", Args: map[string]string{"disk": diskLabel(name), "n": strconv.FormatUint(before-n, 10)}})
		}
	}
	w.inodes = used
	slices.SortFunc(out, func(a, b finding) int { return strings.Compare(a.Args["disk"], b.Args["disk"]) })
	return out
}

func readPlugins(root string) []string {
	files, _ := filepath.Glob(filepath.Join(root, "/boot/config/plugins/*.plg"))
	var out []string
	for _, f := range files {
		out = append(out, strings.TrimSuffix(filepath.Base(f), ".plg"))
	}
	return out
}

// readCron reads the entries of every crontab Unraid builds: cron.d, where it
// writes the schedules of its own settings and plugins, and root's own.
func readCron(root string) []string {
	files, _ := filepath.Glob(filepath.Join(root, "/etc/cron.d/*"))
	files = append(files, filepath.Join(root, "/var/spool/cron/crontabs/root"))
	var out []string
	for _, f := range files {
		file, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(file)
		for sc.Scan() {
			l := strings.TrimSpace(sc.Text())
			if l != "" && !strings.HasPrefix(l, "#") {
				out = append(out, l)
			}
		}
		_ = file.Close()
	}
	return out
}

// readFlash stamps the files of the flash config by size and time of change.
// The plugins are watched on their own, and super.dat changes with every
// start of the array.
func readFlash(root string) map[string]string {
	base := filepath.Join(root, "/boot/config")
	out := map[string]string{}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			rel, _ := filepath.Rel(base, p)
			rel = filepath.ToSlash(rel)
			if e.IsDir() {
				if depth < 2 && rel != "plugins" {
					walk(p, depth+1)
				}
				continue
			}
			if strings.HasSuffix(rel, ".dat") {
				continue
			}
			if fi, err := e.Info(); err == nil {
				out[rel] = strconv.FormatInt(fi.Size(), 10) + "@" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
			}
		}
	}
	walk(base, 0)
	return out
}

// readGuests lists the shares guests reach, as share:SMB or share:NFS. Over
// SMB an exported share lets guests in when it is public or secure, the
// latter to read only. Over NFS it does when it is exported public.
func readGuests(root string) []string {
	files, _ := filepath.Glob(filepath.Join(root, "/boot/config/shares/*.cfg"))
	var out []string
	for _, f := range files {
		var cfg map[string]string
		if secs := ini(f); len(secs) > 0 {
			cfg = secs[0]
		}
		share := strings.TrimSuffix(filepath.Base(f), ".cfg")
		if e := cfg["shareExport"]; e != "" && e != "-" && (cfg["shareSecurity"] == "public" || cfg["shareSecurity"] == "secure") {
			out = append(out, share+":SMB")
		}
		if cfg["shareExportNFS"] == "e" && cfg["shareSecurityNFS"] == "public" {
			out = append(out, share+":NFS")
		}
	}
	slices.Sort(out)
	return out
}

// room follows how full /var/log is: a note once it passes 80 percent, one
// notification once it passes 90, and a word when it has room again, which
// is under 80 and arms both again.
type room struct {
	noted, told bool
}

func (r *room) step(pct int) (note, tell, back bool) {
	if pct < 80 {
		back = r.noted
		r.noted, r.told = false, false
		return false, false, back
	}
	note, r.noted = !r.noted, true
	if pct >= 90 && !r.told {
		tell, r.told = true, true
	}
	return note, tell, false
}

// biggest is the largest file under dir, by its path on the server.
func biggest(root, dir string) (string, int64) {
	var best string
	var size int64
	_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.Mode().IsRegular() && fi.Size() > size {
			rel, _ := filepath.Rel(root, p)
			best, size = "/"+filepath.ToSlash(rel), fi.Size()
		}
		return nil
	})
	return best, size
}
