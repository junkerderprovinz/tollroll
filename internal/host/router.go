package host

import (
	"cmp"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/junkerderprovinz/parleyport/kit/status"
)

// Router is the key of the row of the devices that send their log to this
// server, which in a home network is mostly the router.
const Router = "router"

const (
	// silentAfter is how long a router may send nothing before the watch
	// says so. A quiet network still renews its leases within that time.
	silentAfter = 6 * time.Hour
	// keepDevices bounds the devices kept, like keepAddrs the addresses.
	keepDevices = 2000
	// idsQuiet keeps an attack that goes on from flagging every packet.
	idsQuiet = time.Hour
)

// RouterLog is what the settings page shows of the router log: whether
// Unraid takes the logs of other devices at all, and who sent lines.
type RouterLog struct {
	Server  bool     `json:"server"`
	Senders []Sender `json:"senders"`
}

// Sender is a device whose log Unraid keeps, by the name its lines carry.
type Sender struct {
	Name   string    `json:"name"`
	LastAt time.Time `json:"lastAt,omitzero"`
}

// routerLog is one file Unraid's syslog server writes for a device. disk is
// the mount it lives on, which is read only while it spins.
type routerLog struct {
	addr string
	name string
	disk string
	t    tail
	read bool
}

// rclass is what a router line means. kind picks what the watch does with
// it: login, fail, lease, nat, filter, config or ids.
type rclass struct {
	class
	kind string
}

type routerRule struct {
	msg   *regexp.Regexp
	kind  string
	level status.Severity
	key   string
	names []string
}

func rr(msg, kind string, level status.Severity, key string, names ...string) routerRule {
	return routerRule{msg: regexp.MustCompile(msg), kind: kind, level: level, key: key, names: names}
}

const mac = `([0-9A-Fa-f]{2}(?::[0-9A-Fa-f]{2}){5})`

// routerRules read the lines routers write in the classic syslog form. The
// first are the prosumer routers whose topics lead every line, the rest are
// what the firmware of many home routers shares: its DHCP server, its SSH
// server and its web interface, and the intrusion detection some of them run.
var routerRules = []routerRule{
	rr(`\buser (\S+) logged in from (\S+) via (\S+)`, "login", info, "h_r_login", "user", "addr", "via"),
	rr(`\blogin failure for user (\S+) from (\S+) via (\S+)`, "fail", warn, "h_r_fail", "user", "addr", "via"),
	rr(`\bnat rule (?:added|changed|removed|moved|enabled|disabled) by (\S+)`, "nat", warn, "h_r_nat", "by"),
	rr(`\b(?:filter|mangle|raw) rule (?:added|changed|removed|moved|enabled|disabled) by (\S+)`, "filter", warn, "h_r_filter", "by"),
	rr(`\bassigned (\S+) (?:to|for) `+mac+`(?: (\S+))?`, "lease", info, "h_r_lease", "ip", "mac", "name"),
	rr(`DHCPACK\(\S+\) (\S+) `+mac+`(?: (\S+))?`, "lease", info, "h_r_lease", "ip", "mac", "name"),
	rr(`DHCPACK on (\S+) to `+mac+`(?: \(([^)]+)\))?`, "lease", info, "h_r_lease", "ip", "mac", "name"),
	rr(`(?:Password|Pubkey) auth succeeded for '([^']+)'.* from (\S+?):\d+$`, "login", info, "h_r_login", "user", "addr"),
	rr(`Bad password attempt for '([^']+)' from (\S+?):\d+$`, "fail", warn, "h_r_fail", "user", "addr"),
	rr(`accepted login on \S+ for (\S+) from (\S+)`, "login", info, "h_r_login", "user", "addr"),
	rr(`failed login on \S+ for (\S+) from (\S+)`, "fail", warn, "h_r_fail", "user", "addr"),
	rr(`\[\d+:\d+:\d+\] (.+?) \[Classification: [^\]]*\] \[Priority: \d+\] \{\w+\} (\S+) -> (\S+)`, "ids", warn, "h_r_ids", "sig", "src", "dst"),
}

// classifyRouter words the message of a router line. A line it does not
// know stays in its own words, folded away with the routine ones.
func classifyRouter(msg string) rclass {
	if i := strings.Index(msg, "CEF:"); i >= 0 {
		if c, ok := classifyCEF(msg[i:]); ok {
			return c
		}
	}
	for _, ru := range routerRules {
		m := ru.msg.FindStringSubmatch(msg)
		if m == nil {
			continue
		}
		c := rclass{class: class{Level: ru.level, Key: ru.key, Args: map[string]string{}}, kind: ru.kind}
		for i, name := range ru.names {
			c.Args[name] = m[i+1]
		}
		if by, ok := c.Args["by"]; ok {
			c.Args["user"], c.Args["addr"] = byWhom(by)
			delete(c.Args, "by")
		}
		if c.kind == "lease" {
			c.Args["mac"] = strings.ToLower(c.Args["mac"])
			c.Args["device"] = device(c.Args["name"], c.Args["mac"])
		}
		return c
	}
	return rclass{class: class{Level: info, Quiet: true}}
}

// byWhom splits who made a change, written as a user or as the tool, the
// user and the address, such as winbox-3.41/tcp-msg(winbox):admin@10.0.0.2.
func byWhom(by string) (user, addr string) {
	if i := strings.LastIndex(by, ":"); i >= 0 && strings.Contains(by[i:], "@") {
		by = by[i+1:]
	}
	user, addr, _ = strings.Cut(by, "@")
	return user, addr
}

func device(name, mac string) string {
	if name == "" || name == "*" {
		return mac
	}
	return name + " (" + mac + ")"
}

// classifyCEF reads a line in the Common Event Format, in which gateways of
// the controller kind send their activity log. Its name says what happened,
// and the names vary between versions, so they are matched by their words.
func classifyCEF(s string) (rclass, bool) {
	parts := splitCEF(s)
	if len(parts) < 8 {
		return rclass{}, false
	}
	name, ext := parts[5], cefExtension(parts[7])
	lower := strings.ToLower(name + " " + ext["UNIFIcategory"] + " " + ext["UNIFIsubCategory"])
	arg := func(keys ...string) string {
		for _, k := range keys {
			if v := ext[k]; v != "" {
				return v
			}
		}
		return ""
	}
	user, addr := arg("UNIFIadmin", "suser", "duser"), arg("src", "UNIFIsrcIp")
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(lower, w) {
				return true
			}
		}
		return false
	}
	c := rclass{class: class{Level: info, Args: map[string]string{}}}
	switch {
	case has("threat", "intrusion", "attack"):
		c.kind, c.Level, c.Key = "ids", warn, "h_r_ids"
		c.Args["sig"], c.Args["src"], c.Args["dst"] = cmp.Or(arg("msg"), name), addr, arg("dst", "UNIFIdstIp")
	case has("admin") && has("fail", "denied", "invalid"):
		c.kind, c.Level, c.Key = "fail", warn, "h_r_fail"
		c.Args["user"], c.Args["addr"] = user, addr
	case has("admin") && has("access", "login", "logged in", "signed in"):
		c.kind, c.Key = "login", "h_r_login"
		c.Args["user"], c.Args["addr"] = user, addr
	case has("port forward") && has("add", "chang", "delet", "remov", "updat", "creat", "edit", "enabl", "disabl"):
		c.kind, c.Level, c.Key = "nat", warn, "h_r_nat"
	case has("firewall", "traffic rule", "policy") && has("add", "chang", "delet", "remov", "updat", "creat", "edit", "enabl", "disabl"):
		c.kind, c.Level, c.Key = "filter", warn, "h_r_filter"
	case has("config change", "settings change", "made config", "changed settings"):
		c.kind, c.Level, c.Key = "config", warn, "h_r_config"
	case has("connected") && !has("disconnected") && arg("UNIFIclientMac", "smac") != "":
		c.kind, c.Key = "lease", "h_r_lease"
		c.Args["mac"] = strings.ToLower(arg("UNIFIclientMac", "smac"))
		c.Args["ip"] = arg("UNIFIclientIp", "src")
		c.Args["device"] = device(arg("UNIFIclientAlias", "UNIFIclientHostname", "shost"), c.Args["mac"])
	default:
		c.Quiet = true
		c.Args = nil
	}
	if c.kind != "" && c.kind != "ids" && c.kind != "lease" {
		c.Args["user"], c.Args["addr"] = cmp.Or(c.Args["user"], user), cmp.Or(c.Args["addr"], addr)
	}
	return c, true
}

// splitCEF splits the seven header fields at the pipes CEF does not escape
// and leaves the extension whole.
func splitCEF(s string) []string {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && len(parts) < 7:
			i++
			cur.WriteByte(s[i])
		case s[i] == '|' && len(parts) < 7:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(s[i])
		}
	}
	return append(parts, cur.String())
}

var cefKey = regexp.MustCompile(`(?:^|\s)([A-Za-z0-9_.]+)=`)

// cefExtension reads the key=value pairs of a CEF extension, whose values
// run up to the next key and may hold spaces.
func cefExtension(s string) map[string]string {
	out := map[string]string{}
	at := cefKey.FindAllStringSubmatchIndex(s, -1)
	for i, m := range at {
		end := len(s)
		if i+1 < len(at) {
			end = at[i+1][0]
		}
		out[s[m[2]:m[3]]] = strings.TrimSpace(strings.ReplaceAll(s[m[1]:end], `\=`, "="))
	}
	return out
}

var routerLine = regexp.MustCompile(`^([A-Z][a-z]{2} [ \d]\d \d\d:\d\d:\d\d|\d{4}-\d\d-\d\dT\S+) (\S+) (.*)$`)

// parseRouter reads a line of a file Unraid's syslog server writes: its time,
// the name the device gave and the rest, whose program tag is left in since a
// router does not always write one.
func parseRouter(s string, now time.Time) (at time.Time, name, msg string, ok bool) {
	m := routerLine.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, "", "", false
	}
	at, ok = stamp(m[1], now)
	return at, m[2], m[3], ok
}

// findRouterLogs looks for the files Unraid's syslog server keeps, as its
// settings in rsyslog.cfg name them, and leaves out the server's own. A file
// on a disk that is spun down is not looked for, and one already known stays
// while its disk sleeps.
func (h *Host) findRouterLogs() {
	var cfg map[string]string
	if secs := ini(h.path("/boot/config/rsyslog.cfg")); len(secs) > 0 {
		cfg = secs[0]
	}
	h.routerOn = cfg["local_server"] != "" && cfg["server_folder"] != ""
	if !h.routerOn {
		h.routerLogs = nil
		return
	}
	pre, post, ok := strings.Cut(cmp.Or(cfg["server_filename"], "syslog-%FROMHOST-IP%.log"), "%")
	if !ok {
		return
	}
	if i := strings.Index(post, "%"); i >= 0 {
		post = post[i+1:]
	}
	if h.routerLogs == nil {
		h.routerLogs = map[string]*routerLog{}
	}
	own := h.own()
	for _, d := range h.logDirs(cfg["server_folder"]) {
		if h.asleep(d.disk) {
			continue
		}
		seen := map[string]bool{}
		files, _ := filepath.Glob(filepath.Join(d.dir, globEscape(pre)+"*"+globEscape(post)))
		for _, f := range files {
			addr := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), pre), post)
			if own[addr] {
				continue
			}
			seen[f] = true
			if _, ok := h.routerLogs[f]; !ok {
				h.routerLogs[f] = &routerLog{addr: addr, name: addr, disk: d.disk, t: tail{path: f}}
			}
		}
		for f, rl := range h.routerLogs {
			if rl.disk == d.disk && filepath.Dir(f) == filepath.Clean(d.dir) && !seen[f] {
				delete(h.routerLogs, f)
			}
		}
	}
}

func globEscape(s string) string {
	return strings.NewReplacer(`*`, `\*`, `?`, `\?`, `[`, `\[`).Replace(s)
}

type logDir struct{ dir, disk string }

// logDirs resolves a folder to the folders on the disks that hold it. A share
// path goes through the disks themselves, since a read through the user
// share could wake a disk that holds nothing of it.
func (h *Host) logDirs(folder string) []logDir {
	for _, share := range []string{"/mnt/user/", "/mnt/user0/"} {
		rel, ok := strings.CutPrefix(folder, share)
		if !ok {
			continue
		}
		var out []logDir
		for mnt := range h.probe.mounts() {
			name, ok := strings.CutPrefix(mnt, "/mnt/")
			if ok && !strings.Contains(name, "/") && !excluded[mnt] {
				out = append(out, logDir{dir: h.path(mnt + "/" + rel), disk: name})
			}
		}
		slices.SortFunc(out, func(a, b logDir) int { return strings.Compare(a.dir, b.dir) })
		return out
	}
	disk := ""
	if rest, ok := strings.CutPrefix(folder, "/mnt/"); ok {
		disk, _, _ = strings.Cut(rest, "/")
	}
	return []logDir{{dir: h.path(folder), disk: disk}}
}

// asleep reports whether the disk mounted as name, or a disk of the pool of
// that name, is spun down.
func (h *Host) asleep(name string) bool {
	for _, d := range h.disks {
		if d.Down && (d.Name == name || strings.TrimRightFunc(d.Name, unicode.IsDigit) == name) {
			return true
		}
	}
	return false
}

// own are the names under which the server's own lines come back, when
// Unraid sends its log to itself.
func (h *Host) own() map[string]bool {
	out := map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true}
	for _, a := range h.o.Own() {
		out[a] = true
	}
	return out
}

func ownAddrs() []string {
	var out []string
	if name, err := os.Hostname(); err == nil {
		out = append(out, name)
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ip, _, err := net.ParseCIDR(a.String()); err == nil {
			out = append(out, ip.String())
		}
	}
	return out
}

// readRouters reads on in every router file whose disk is awake. The first
// read of a file teaches the watch what is normal without telling, apart
// from the lines newer than the last one it saw of that device.
func (h *Host) readRouters(now time.Time) {
	for _, f := range slices.Sorted(func(yield func(string) bool) {
		for f := range h.routerLogs {
			if !yield(f) {
				return
			}
		}
	}) {
		rl := h.routerLogs[f]
		if h.asleep(rl.disk) {
			continue
		}
		since := h.watch.base.Routers[rl.addr]
		first := !rl.read
		rl.t.read(func(s string) {
			at, name, msg, ok := parseRouter(s, now)
			if !ok {
				return
			}
			live := !first || (!since.IsZero() && at.After(since))
			h.routerLine(rl, at, name, msg, live, now)
		})
		rl.read = true
	}
	for _, rl := range h.routerLogs {
		last := h.watch.base.Routers[rl.addr]
		if last.IsZero() || now.Sub(last) < silentAfter || h.silent[rl.addr] {
			continue
		}
		h.silent[rl.addr] = true
		h.found(Router, finding{Level: warn, Key: "h_r_silent", Args: map[string]string{"router": rl.name, "h": strconv.Itoa(int(silentAfter.Hours()))}}, now)
	}
}

func (h *Host) routerLine(rl *routerLog, at time.Time, name, msg string, live bool, now time.Time) {
	w := &h.watch.base
	if w.Routers == nil {
		w.Routers = map[string]time.Time{}
	}
	if at.After(w.Routers[rl.addr]) {
		w.Routers[rl.addr] = at
	}
	rl.name = name
	if live && h.silent[rl.addr] {
		delete(h.silent, rl.addr)
		h.drop("h_r_silent", rl.name)
		h.add(entry{at: at, app: Router, level: info, key: "h_r_back", args: map[string]string{"router": rl.name}})
	}
	c := classifyRouter(msg)
	if c.Args != nil {
		c.Args["router"] = name
	}
	e := entry{at: at, app: Router, level: c.Level, quiet: c.Quiet, key: c.Key, args: c.Args, raw: name + ": " + msg}
	recent := now.Sub(at) < needFor
	switch c.kind {
	case "login":
		addr := c.Args["addr"]
		_, known := w.Admins[addr]
		if !known {
			if w.Admins == nil {
				w.Admins = map[string]time.Time{}
			}
			w.Admins[addr] = at
			trim(w.Admins, keepAddrs)
			h.dirty = true
		}
		e.quiet = known || !live
		if !known && live {
			h.found(Router, finding{Level: warn, Key: "h_r_new_login", Args: c.Args}, at)
		}
	case "fail":
		addr := c.Args["addr"]
		fails := slices.DeleteFunc(append(h.routerFails[addr], at), func(t time.Time) bool { return at.Sub(t) >= burstWindow })
		h.routerFails[addr] = fails
		if live && len(fails) >= burst && at.Sub(h.routerTold[addr]) >= burstQuiet {
			h.routerTold[addr] = at
			h.found(Router, finding{Level: warn, Key: "h_r_fails", Args: map[string]string{"router": name, "addr": addr, "n": strconv.Itoa(len(fails))}}, at)
		}
	case "lease":
		m := c.Args["mac"]
		_, known := w.Devices[m]
		if w.Devices == nil {
			w.Devices = map[string]time.Time{}
		}
		if !known {
			h.dirty = true
		}
		w.Devices[m] = at
		trim(w.Devices, keepDevices)
		e.quiet = true
		if !known && live {
			h.found(Router, finding{Level: warn, Key: "h_r_new_device", Args: c.Args}, at)
		}
	case "nat", "filter", "config":
		if live && recent {
			h.watched(e)
			return
		}
	case "ids":
		sig := c.Args["sig"]
		if live && recent && at.Sub(h.idsTold[sig]) >= idsQuiet {
			h.idsTold[sig] = at
			h.watched(e)
			return
		}
	}
	h.add(e)
}

// trim drops the oldest entries of m until it holds n.
func trim(m map[string]time.Time, n int) {
	for len(m) > n {
		var oldest string
		for k, t := range m {
			if oldest == "" || t.Before(m[oldest]) {
				oldest = k
			}
		}
		delete(m, oldest)
	}
}

// drop takes the need of key about router off its row.
func (h *Host) drop(key, router string) {
	h.needs = slices.DeleteFunc(h.needs, func(e entry) bool { return e.key == key && e.args["router"] == router })
}

// RouterLog returns what the settings page shows of the router log.
func (h *Host) RouterLog() RouterLog {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := RouterLog{Server: h.routerOn, Senders: []Sender{}}
	for _, rl := range h.routerLogs {
		out.Senders = append(out.Senders, Sender{Name: rl.name, LastAt: h.watch.base.Routers[rl.addr]})
	}
	slices.SortFunc(out.Senders, func(a, b Sender) int { return strings.Compare(a.Name, b.Name) })
	return out
}
