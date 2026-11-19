package host

import (
	"bufio"
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/junkerderprovinz/parleyport/kit/notify"
)

// rights reads the special rights a container runs with from its
// hostconfig.json: privileged, the host's network and Docker's socket.
func rights(dir string, mounts []string) []string {
	var hc struct {
		Privileged  bool
		NetworkMode string
		Binds       []string
	}
	b, err := os.ReadFile(filepath.Join(dir, "hostconfig.json"))
	if err != nil || json.Unmarshal(b, &hc) != nil {
		return nil
	}
	var out []string
	if hc.Privileged {
		out = append(out, "privileged")
	}
	if hc.NetworkMode == "host" {
		out = append(out, "host")
	}
	for _, src := range append(mounts, hc.Binds...) {
		src, _, _ = strings.Cut(src, ":")
		if src == "/var/run/docker.sock" || src == "/run/docker.sock" {
			out = append(out, "socket")
			break
		}
	}
	return out
}

// privileged learns which containers hold which special rights, each given
// as name|right, and returns one finding for every right that is new.
func (w *watch) privileged(held []string) []finding {
	var out []finding
	for _, r := range w.added(&w.base.Priv, held) {
		name, right, _ := strings.Cut(r, "|")
		out = append(out, finding{Level: warn, Key: "h_w_priv", Args: map[string]string{"name": name, "right": "@h_right_" + right}})
	}
	return out
}

const (
	// ioRing is how many looks make the hour a disk's traffic is judged
	// by, ioLearn how many such hours the watch learns from before it
	// judges at all, and ioWeek how far back its sense of normal reaches.
	ioRing  = int(time.Hour / slowEvery)
	ioLearn = 2 * 24 * ioRing
	ioWeek  = 7 * 24 * ioRing
	// ioFactor and ioFloor make an hour unusual: that many times the usual
	// and at least that much.
	ioFactor = 4
	ioFloor  = 4 << 30
	ioQuiet  = 6 * time.Hour
)

// norm is what a disk usually reads or writes in an hour.
type norm struct {
	Mean float64 `json:"mean"`
	N    int     `json:"n"`
}

// io follows what each disk read and wrote, by its counters in bytes keyed
// disk|read and disk|write, and reports an hour far above its usual. busy is
// a parity check or the mover, which move far more on purpose; the watch
// neither judges nor learns from them.
func (w *watch) io(now time.Time, counters map[string]uint64, busy bool) []finding {
	if w.ioRing == nil {
		w.ioRing, w.ioTold = map[string][]uint64{}, map[string]time.Time{}
	}
	gap := now.Sub(w.ioAt)
	fresh := w.ioAt.IsZero() || gap <= 0 || gap > 4*slowEvery
	last := w.ioLast
	w.ioLast, w.ioAt = counters, now
	var out []finding
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range counters {
			if !yield(k) {
				return
			}
		}
	}) {
		before, ok := last[k]
		if fresh || !ok || counters[k] < before {
			delete(w.ioRing, k)
			continue
		}
		ring := append(w.ioRing[k], counters[k]-before)
		if len(ring) > ioRing {
			ring = ring[len(ring)-ioRing:]
		}
		w.ioRing[k] = ring
		if len(ring) < ioRing || busy {
			continue
		}
		var hour uint64
		for _, d := range ring {
			hour += d
		}
		if w.base.IO == nil {
			w.base.IO = map[string]norm{}
		}
		n := w.base.IO[k]
		if n.N >= ioLearn && hour >= ioFloor && float64(hour) >= ioFactor*max(n.Mean, 1) && now.Sub(w.ioTold[k]) >= ioQuiet {
			w.ioTold[k] = now
			disk, dir, _ := strings.Cut(k, "|")
			out = append(out, finding{Level: warn, Key: "h_w_io_" + dir, Args: map[string]string{
				"disk": diskLabel(disk), "amount": human(int64(hour)), "x": strconv.Itoa(int(float64(hour) / max(n.Mean, 1)))}})
		}
		n.N++
		n.Mean += (float64(hour) - n.Mean) / float64(min(n.N, ioWeek))
		w.base.IO[k] = n
	}
	// The usual changes a little every look, and the flash drive hears of it
	// once an hour.
	if now.Sub(w.ioSaved) >= time.Hour {
		w.ioSaved, w.changed = now, true
	}
	return out
}

// readIO reads the bytes each disk of the array and the pools read and wrote
// since boot, from /proc/diskstats, keyed disk|read and disk|write.
func readIO(root string) map[string]uint64 {
	names := map[string]string{}
	for _, sec := range ini(filepath.Join(root, "/var/local/emhttp/disks.ini")) {
		if sec["device"] != "" && sec["name"] != "" && sec["type"] != "Flash" {
			names[sec["device"]] = sec["name"]
		}
	}
	f, err := os.Open(filepath.Join(root, "/proc/diskstats"))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	out := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 10 {
			continue
		}
		name, ok := names[fs[2]]
		if !ok {
			continue
		}
		read, _ := strconv.ParseUint(fs[5], 10, 64)
		written, _ := strconv.ParseUint(fs[9], 10, 64)
		out[name+"|read"], out[name+"|write"] = read*512, written*512
	}
	return out
}

// snapPools names the pools that hold snapshots, ZFS or btrfs, by their file
// system, leaving out any with a disk spun down: listing its snapshots could
// wake it.
func snapPools(root string) map[string]string {
	secs := ini(filepath.Join(root, "/var/local/emhttp/disks.ini"))
	names := map[string]bool{}
	for _, sec := range secs {
		names[sec["name"]] = true
	}
	out, asleep := map[string]string{}, map[string]bool{}
	for _, sec := range secs {
		pool := sec["name"]
		if cut := strings.TrimRightFunc(pool, unicode.IsDigit); cut != pool && names[cut] {
			pool = cut
		}
		if sec["spundown"] == "1" {
			asleep[pool] = true
		}
		if fs := sec["fsType"]; fs == "zfs" || fs == "btrfs" {
			out[pool] = fs
		}
	}
	for p := range asleep {
		delete(out, p)
	}
	return out
}

// listSnaps lists the snapshots of each pool: ZFS's as pool/dataset@name,
// btrfs's as the pool and the path of the snapshot. A pool that cannot be
// read is left out.
func listSnaps(pools map[string]string) map[string][]string {
	out := map[string][]string{}
	for pool, fs := range pools {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		var cmd *exec.Cmd
		if fs == "zfs" {
			cmd = exec.CommandContext(ctx, "zfs", "list", "-H", "-t", "snapshot", "-o", "name", "-r", pool)
		} else {
			cmd = exec.CommandContext(ctx, "btrfs", "subvolume", "list", "-s", "/mnt/"+pool)
		}
		b, err := cmd.Output()
		cancel()
		if err != nil {
			continue
		}
		var snaps []string
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if fs == "btrfs" {
				_, p, ok := strings.Cut(l, " path ")
				if !ok {
					continue
				}
				l = pool + "/" + strings.TrimPrefix(p, "<FS_TREE>/")
			}
			if l != "" {
				snaps = append(snaps, l)
			}
		}
		out[pool] = snaps
	}
	return out
}

// snapFS is the file system a snapshot belongs to, and its own name.
func snapFS(snap string) (fs, name string) {
	if fs, name, ok := strings.Cut(snap, "@"); ok {
		return fs, name
	}
	return path.Dir(snap), path.Base(snap)
}

// snapshots compares the snapshots of each pool with the last look and
// returns the ones gone, by their file system. A pool missing from either
// look says nothing, since it may only be unmounted.
func (w *watch) snapshots(now map[string][]string) map[string][]string {
	gone := map[string][]string{}
	next := map[string]map[string]bool{}
	for pool, snaps := range now {
		set := map[string]bool{}
		for _, s := range snaps {
			set[s] = true
		}
		next[pool] = set
		for s := range w.snaps[pool] {
			if !set[s] {
				fs, name := snapFS(s)
				gone[fs] = append(gone[fs], name)
			}
		}
	}
	w.snaps = next
	return gone
}

// atQueue is where Slackware's at keeps the jobs that wait.
const atQueue = "/var/spool/atjobs"

var heredoc = regexp.MustCompile(`<< '(marcinDELIMITER\w+)'$`)

// readAt reads the command of every job in the at queue, as one line.
func readAt(root string) []string {
	files, _ := filepath.Glob(filepath.Join(root, atQueue, "*"))
	var out []string
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), ".") {
			continue
		}
		if fi, err := os.Stat(f); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if job := atCommand(string(b)); job != "" {
			out = append(out, job)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// atCommand finds the command in a job file: what follows the block that
// changes into the job's directory, inside the heredoc newer versions of at
// wrap it in.
func atCommand(job string) string {
	lines := strings.Split(job, "\n")
	start := 0
	for i, l := range lines {
		if strings.TrimSpace(l) == "}" {
			start = i + 1
		}
	}
	var cmd []string
	end := ""
	for _, l := range lines[start:] {
		l = strings.TrimSpace(l)
		switch {
		case l == "" || strings.HasPrefix(l, "#"):
		case end == "" && heredoc.MatchString(l):
			end = heredoc.FindStringSubmatch(l)[1]
		case end != "" && l == end:
			end = ""
		default:
			cmd = append(cmd, l)
		}
	}
	s := strings.Join(cmd, "; ")
	if r := []rune(s); len(r) > 160 {
		s = string(r[:159]) + "…"
	}
	return s
}

func (w *watch) at(jobs []string) []finding {
	var out []finding
	for _, j := range w.added(&w.base.At, jobs) {
		out = append(out, finding{Level: warn, Key: "h_w_at", Args: map[string]string{"job": j}})
	}
	return out
}

// snapDue names the pools whose snapshots to look at now, every few minutes.
// Called with mu held.
func (h *Host) snapDue() map[string]string {
	now := h.o.Now()
	if !h.unraid || now.Sub(h.snapAt) < slowEvery {
		return nil
	}
	h.snapAt = now
	return snapPools(h.o.Root)
}

// vanished lists the snapshots of pools and reports the ones gone that no app
// of the fortress logged removing. Listing them and asking the fortress take
// time, so neither holds mu.
func (h *Host) vanished(pools map[string]string) []notify.Event {
	if len(pools) == 0 {
		return nil
	}
	snaps := h.o.Snaps(pools)
	h.mu.Lock()
	gone := h.watch.snapshots(snaps)
	h.mu.Unlock()
	unclaimed := map[string]int{}
	for fs, names := range gone {
		for _, n := range names {
			if !h.o.Claimed(n) {
				unclaimed[fs]++
			}
		}
	}
	if len(unclaimed) == 0 {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.o.Now()
	for _, fs := range slices.Sorted(maps.Keys(unclaimed)) {
		h.found(Unraid, finding{Level: warn, Key: "h_w_snap", Args: map[string]string{"fs": fs, "n": strconv.Itoa(unclaimed[fs])}}, now)
	}
	tell := h.alarms
	h.alarms = nil
	return tell
}
