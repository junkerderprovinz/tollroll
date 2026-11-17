package host

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// disk is a spinning disk of the array or a pool. Down is Unraid's own word
// on whether it is spun down.
type disk struct {
	Name   string
	Device string
	Down   bool
}

// readDisks reads emhttpd's disks.ini, which Unraid keeps up to date without
// asking any disk. Flash, empty slots and SSDs are left out: none of them
// spins up.
func readDisks(root string) []disk {
	var out []disk
	for _, sec := range ini(filepath.Join(root, "/var/local/emhttp/disks.ini")) {
		if sec["device"] == "" || sec["rotational"] != "1" || sec["type"] == "Flash" {
			continue
		}
		out = append(out, disk{Name: sec["name"], Device: sec["device"], Down: sec["spundown"] == "1"})
	}
	return out
}

// ini reads the sections of one of emhttpd's ini files in order. A file
// without sections is one section.
func ini(p string) []map[string]string {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []map[string]string
	cur := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		s := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(s, "[") {
			if len(cur) > 0 {
				out = append(out, cur)
			}
			cur = map[string]string{}
			continue
		}
		k, v, ok := strings.Cut(s, "=")
		if ok {
			cur[k] = strings.Trim(v, `"`)
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// spins follows which devices are spun down, from disks.ini and from the
// syslog lines emhttpd writes when it spins one down and when it reads the
// SMART data of one that came back. Either can see a wake first, and it
// counts once.
type spins struct {
	isDown map[string]bool
}

func (s *spins) down(dev string) {
	if s.isDown == nil {
		s.isDown = map[string]bool{}
	}
	s.isDown[dev] = true
}

// up reports whether dev was down, that is whether it just woke.
func (s *spins) up(dev string) bool {
	woke := s.isDown[dev]
	delete(s.isDown, dev)
	return woke
}

func (s *spins) look(disks []disk) []disk {
	var woke []disk
	for _, d := range disks {
		if d.Down {
			s.down(d.Device)
		} else if s.up(d.Device) {
			woke = append(woke, d)
		}
	}
	return woke
}

const dayFormat = "2006-01-02"

// tally counts wake-ups by day and disk name.
type tally map[string]map[string]int

func (t tally) add(at time.Time, name string) {
	day := at.Format(dayFormat)
	if t[day] == nil {
		t[day] = map[string]int{}
	}
	t[day][name]++
}

// counts returns the wake-ups of name on the day of now and on it and the six
// days before.
func (t tally) counts(name string, now time.Time) (today, week int) {
	for i := range 7 {
		n := t[now.AddDate(0, 0, -i).Format(dayFormat)][name]
		if i == 0 {
			today = n
		}
		week += n
	}
	return today, week
}

func (t tally) prune(now time.Time) {
	oldest := now.AddDate(0, 0, -6).Format(dayFormat)
	for day := range t {
		if day < oldest {
			delete(t, day)
		}
	}
}

// cause is who most likely did something, by the kind of thing it is and its
// name. By is one of container, vm, smb, smb_any, nfs, mover, parity, task,
// process or unknown, and picks the phrase that names it.
type cause struct {
	By  string
	Who string
}

var unknown = cause{By: "unknown"}

// probe finds causes in /proc and Unraid's state files under root. It only
// reads what the kernel and Unraid keep in memory, so it never touches a
// disk, and it is only asked once something happened.
type probe struct {
	root string
}

func (p probe) path(s string) string { return filepath.Join(p.root, s) }

// cause names what most likely woke d. A parity check reads every disk of the
// array. Otherwise a program with a file open on the disk is the best
// witness, and when that is the user share layer, the program with the same
// file open on a user share. Failing that the running mover, then an NFS
// client with the file open.
func (p probe) cause(d disk) cause {
	array := strings.HasPrefix(d.Name, "disk") || strings.HasPrefix(d.Name, "parity")
	if array && p.parityRunning() {
		return cause{By: "parity"}
	}
	mnt := p.mount(d.Name)
	if mnt == "" {
		return unknown
	}
	open := p.openFiles()
	var witnesses []int
	shared := map[string]bool{}
	for pid, files := range open {
		for _, f := range files {
			rel, ok := under(f, mnt)
			if !ok {
				continue
			}
			if p.comm(pid) == "shfs" {
				shared[rel] = true
				continue
			}
			witnesses = append(witnesses, pid)
			break
		}
	}
	for pid, files := range open {
		for _, f := range files {
			for _, user := range []string{"/mnt/user", "/mnt/user0"} {
				if rel, ok := under(f, user); ok && shared[rel] && p.comm(pid) != "shfs" {
					witnesses = append(witnesses, pid)
				}
			}
		}
	}
	if c, ok := p.best(witnesses); ok {
		return c
	}
	if p.moverRunning() {
		return cause{By: "mover"}
	}
	if addr := p.nfsClient(mnt, shared); addr != "" {
		return cause{By: "nfs", Who: addr}
	}
	return unknown
}

// writer names what has file open, for the biggest file in /var/log. An smbd
// that writes there writes its own log, for no client.
func (p probe) writer(file string) cause {
	var pids []int
	for pid, files := range p.openFiles() {
		for _, f := range files {
			if f == file {
				pids = append(pids, pid)
				break
			}
		}
	}
	c, ok := p.best(pids)
	switch {
	case !ok:
		return unknown
	case c.By == "smb" || c.By == "smb_any":
		return cause{By: "process", Who: "smbd"}
	}
	return c
}

// rank orders the kinds of cause from the most telling: a container or VM
// names what the user runs, a bare program name says the least.
var rank = map[string]int{"container": 0, "vm": 1, "smb": 2, "smb_any": 3, "task": 4, "mover": 5, "process": 6}

func (p probe) best(pids []int) (cause, bool) {
	var out cause
	found := false
	for _, pid := range pids {
		c := p.who(pid)
		if !found || rank[c.By] < rank[out.By] || (rank[c.By] == rank[out.By] && c.Who < out.Who) {
			out, found = c, true
		}
	}
	return out, found
}

func under(file, dir string) (string, bool) {
	if file == dir {
		return ".", true
	}
	rel, ok := strings.CutPrefix(file, dir+"/")
	return rel, ok
}

var dockerID = regexp.MustCompile(`docker[-/]([0-9a-f]{64})`)

// who is the cause pid stands for.
func (p probe) who(pid int) cause {
	if m := dockerID.FindStringSubmatch(p.read(pid, "cgroup")); m != nil {
		return cause{By: "container", Who: p.container(m[1])}
	}
	comm := p.comm(pid)
	switch {
	case strings.HasPrefix(comm, "qemu-system"):
		return cause{By: "vm", Who: vmName(p.read(pid, "cmdline"))}
	case comm == "smbd":
		if addr := p.smbClient(pid); addr != "" {
			return cause{By: "smb", Who: addr}
		}
		return cause{By: "smb_any"}
	}
	for up, seen := p.parent(pid), 0; up > 1 && seen < 16; up, seen = p.parent(up), seen+1 {
		switch p.comm(up) {
		case "crond", "cron":
			return cause{By: "task", Who: comm}
		case "mover", "move":
			return cause{By: "mover"}
		}
	}
	if comm == "mover" || comm == "move" {
		return cause{By: "mover"}
	}
	return cause{By: "process", Who: comm}
}

func (p probe) read(pid int, name string) string {
	b, err := os.ReadFile(p.path(fmt.Sprintf("/proc/%d/%s", pid, name)))
	if err != nil {
		return ""
	}
	return string(b)
}

func (p probe) comm(pid int) string { return strings.TrimSpace(p.read(pid, "comm")) }

func (p probe) parent(pid int) int {
	for _, l := range strings.Split(p.read(pid, "status"), "\n") {
		if v, ok := strings.CutPrefix(l, "PPid:"); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		}
	}
	return 0
}

// container is the name Docker shows for id, or its short id.
func (p probe) container(id string) string {
	var c struct{ Name string }
	b, err := os.ReadFile(p.path("/var/lib/docker/containers/" + id + "/config.v2.json"))
	if err == nil && json.Unmarshal(b, &c) == nil && c.Name != "" {
		return strings.TrimPrefix(c.Name, "/")
	}
	return id[:12]
}

// vmName reads the guest name libvirt hands qemu as -name guest=NAME,...
func vmName(cmdline string) string {
	args := strings.Split(cmdline, "\x00")
	for i, a := range args {
		if a == "-name" && i+1 < len(args) {
			v := strings.TrimPrefix(args[i+1], "guest=")
			name, _, _ := strings.Cut(v, ",")
			return name
		}
	}
	return "?"
}

// openFiles lists the files every process has open, by pid. A process that
// ends while it is read is left out.
func (p probe) openFiles() map[int][]string {
	out := map[int][]string{}
	entries, _ := os.ReadDir(p.path("/proc"))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		dir := p.path(fmt.Sprintf("/proc/%d/fd", pid))
		fds, _ := os.ReadDir(dir)
		for _, fd := range fds {
			if target, err := os.Readlink(filepath.Join(dir, fd.Name())); err == nil {
				out[pid] = append(out[pid], target)
			}
		}
	}
	return out
}

// mount is where the disk name is mounted: /mnt/disk1 for an array disk, and
// for the second device of a pool, cache2, the pool's /mnt/cache.
func (p probe) mount(name string) string {
	points := p.mounts()
	for _, n := range []string{name, strings.TrimRight(name, "0123456789")} {
		if _, ok := points["/mnt/"+n]; ok {
			return "/mnt/" + n
		}
	}
	return ""
}

// mounts maps each mount point to its device numbers as "major:minor".
func (p probe) mounts() map[string]string {
	out := map[string]string{}
	b, _ := os.ReadFile(p.path("/proc/self/mountinfo"))
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) > 4 {
			out[f[4]] = f[2]
		}
	}
	return out
}

func (p probe) parityRunning() bool {
	for _, sec := range ini(p.path("/var/local/emhttp/var.ini")) {
		if pos, ok := sec["mdResyncPos"]; ok {
			return pos != "" && pos != "0"
		}
	}
	return false
}

func (p probe) moverRunning() bool {
	b, err := os.ReadFile(p.path("/var/run/mover.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return false
	}
	_, err = os.Stat(p.path(fmt.Sprintf("/proc/%d", pid)))
	return err == nil
}

// smbClient is the address of the client an smbd process serves, from the
// TCP connection on port 445 among its sockets.
func (p probe) smbClient(pid int) string {
	inodes := map[string]bool{}
	dir := p.path(fmt.Sprintf("/proc/%d/fd", pid))
	fds, _ := os.ReadDir(dir)
	for _, fd := range fds {
		target, _ := os.Readlink(filepath.Join(dir, fd.Name()))
		if ino, ok := strings.CutPrefix(target, "socket:["); ok {
			inodes[strings.TrimSuffix(ino, "]")] = true
		}
	}
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, _ := os.ReadFile(p.path(table))
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) < 10 || !inodes[f[9]] || !strings.HasSuffix(f[1], ":01BD") {
				continue
			}
			if addr, ok := hexAddr(strings.Split(f[2], ":")[0]); ok {
				return addr
			}
		}
	}
	return ""
}

// hexAddr reads an address the way /proc/net/tcp writes it: each 32-bit
// word in host order, which is little endian on the servers Unraid runs on.
func hexAddr(h string) (string, bool) {
	if len(h) != 8 && len(h) != 32 {
		return "", false
	}
	var b []byte
	for w := 0; w < len(h); w += 8 {
		word, err := strconv.ParseUint(h[w:w+8], 16, 32)
		if err != nil {
			return "", false
		}
		b = append(b, byte(word), byte(word>>8), byte(word>>16), byte(word>>24))
	}
	a, ok := netip.AddrFromSlice(b)
	if !ok {
		return "", false
	}
	return a.Unmap().String(), true
}

var (
	nfsAddr  = regexp.MustCompile(`(?m)^address: "(.+):\d+"`)
	nfsState = regexp.MustCompile(`superblock: "([0-9a-f]+):([0-9a-f]+):\d+", filename: "([^"]*)"`)
)

// nfsClient is the address of an NFS client with a file open on the disk
// mounted at mnt: one on the disk itself, or, through a user share, one the
// user share layer holds open there. The kernel lists open NFSv4 files with
// their superblock in hex; mountinfo has the same numbers in decimal.
func (p probe) nfsClient(mnt string, shared map[string]bool) string {
	points := p.mounts()
	names := map[string]bool{}
	for rel := range shared {
		names[path.Base(rel)] = true
	}
	clients, _ := filepath.Glob(p.path("/proc/fs/nfsd/clients/*"))
	for _, c := range clients {
		info, _ := os.ReadFile(filepath.Join(c, "info"))
		addr := nfsAddr.FindSubmatch(info)
		states, _ := os.ReadFile(filepath.Join(c, "states"))
		if addr == nil {
			continue
		}
		for _, m := range nfsState.FindAllStringSubmatch(string(states), -1) {
			major, _ := strconv.ParseUint(m[1], 16, 32)
			minor, _ := strconv.ParseUint(m[2], 16, 32)
			dev := fmt.Sprintf("%d:%d", major, minor)
			if dev == points[mnt] || (dev == points["/mnt/user"] && names[m[3]]) {
				return strings.Trim(string(addr[1]), "[]")
			}
		}
	}
	return ""
}
