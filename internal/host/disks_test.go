package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// write puts a file under root at its path on the server.
func write(t *testing.T, root, path, body string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type proc struct {
	pid, ppid int
	comm      string
	cgroup    string
	cmdline   []string
	fds       []string
}

// process lays out pid under root/proc the way the kernel shows it, its open
// files as links that need not resolve.
func process(t *testing.T, root string, p proc) {
	t.Helper()
	dir := fmt.Sprintf("/proc/%d", p.pid)
	write(t, root, dir+"/comm", p.comm+"\n")
	write(t, root, dir+"/status", fmt.Sprintf("Name:\t%s\nPPid:\t%d\n", p.comm, p.ppid))
	write(t, root, dir+"/cgroup", p.cgroup+"\n")
	write(t, root, dir+"/cmdline", strings.Join(p.cmdline, "\x00")+"\x00")
	fd := filepath.Join(root, dir, "fd")
	if err := os.MkdirAll(fd, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, target := range p.fds {
		if err := os.Symlink(target, filepath.Join(fd, strconv.Itoa(i+3))); err != nil {
			t.Fatal(err)
		}
	}
}

const disksIni = `["parity"]
idx="0"
name="parity"
device="sdb"
rotational="1"
spundown="1"
type="Parity"
["disk1"]
idx="1"
name="disk1"
device="sdc"
rotational="1"
spundown="0"
type="Data"
fsType="xfs"
["disk2"]
idx="2"
name="disk2"
device=""
rotational="1"
type="Data"
["cache"]
name="cache"
device="nvme0n1"
rotational="0"
spundown="0"
type="Cache"
["backup"]
name="backup"
device="sdf"
rotational="1"
spundown="1"
type="Cache"
["flash"]
name="flash"
device="sda"
rotational="1"
type="Flash"
`

const mountinfo = `46 1 0:30 / / rw,relatime - rootfs rootfs rw
51 46 9:1 / /mnt/disk1 rw,noatime - xfs /dev/md1p1 rw
55 46 259:1 / /mnt/cache rw,noatime - xfs /dev/nvme0n1p1 rw
56 46 8:81 / /mnt/backup rw,noatime - xfs /dev/sdf1 rw
58 46 0:45 / /mnt/user rw,nosuid - fuse.shfs shfs rw
`

func unraid(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "/var/local/emhttp/disks.ini", disksIni)
	write(t, root, "/var/local/emhttp/var.ini", "mdState=\"STARTED\"\nmdResyncPos=\"0\"\n")
	write(t, root, "/proc/self/mountinfo", mountinfo)
	process(t, root, proc{pid: 1, comm: "init", cgroup: "0::/"})
	process(t, root, proc{pid: 900, ppid: 1, comm: "shfs", cgroup: "0::/"})
	return root
}

func TestOnlyTheSpinningDisksOfArrayAndPoolsAreWatched(t *testing.T) {
	got := readDisks(unraid(t))
	want := []disk{{Name: "parity", Device: "sdb", Down: true}, {Name: "disk1", Device: "sdc"}, {Name: "backup", Device: "sdf", Down: true}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%+v", got)
	}
}

func TestAWakeIsADiskThatWasDownAndIsUpAgain(t *testing.T) {
	var s spins
	d := disk{Name: "disk1", Device: "sdc", Down: true}
	if w := s.look([]disk{d}); len(w) != 0 {
		t.Errorf("first look: %v", w)
	}
	d.Down = false
	if w := s.look([]disk{d}); len(w) != 1 || w[0].Name != "disk1" {
		t.Errorf("woke: %v", w)
	}
	if w := s.look([]disk{d}); len(w) != 0 {
		t.Errorf("still up: %v", w)
	}
}

func TestADiskUpAtTheFirstLookDidNotWake(t *testing.T) {
	var s spins
	if w := s.look([]disk{{Name: "disk1", Device: "sdc"}}); len(w) != 0 {
		t.Errorf("%v", w)
	}
	if s.up("sdc") {
		t.Error("a SMART read of a disk that was never down counts as a wake")
	}
}

func TestASpinUpSeenInTheSyslogCountsOnce(t *testing.T) {
	var s spins
	s.look([]disk{{Name: "disk1", Device: "sdc"}})
	s.down("sdc")
	if !s.up("sdc") {
		t.Fatal("the SMART read after a spin down is no wake")
	}
	if w := s.look([]disk{{Name: "disk1", Device: "sdc"}}); len(w) != 0 {
		t.Errorf("the state file counted it again: %v", w)
	}
}

func TestWakeUpsAreCountedForTodayAndTheLastSevenDays(t *testing.T) {
	day := func(n int) time.Time { return now.AddDate(0, 0, -n) }
	tl := tally{}
	tl.add(day(0), "disk1")
	tl.add(day(0), "disk1")
	tl.add(day(3), "disk1")
	tl.add(day(6), "disk1")
	tl.add(day(7), "disk1")
	tl.add(day(0), "disk2")
	if today, week := tl.counts("disk1", now); today != 2 || week != 4 {
		t.Errorf("disk1: %d today, %d in 7 days", today, week)
	}
	if today, week := tl.counts("parity", now); today != 0 || week != 0 {
		t.Errorf("parity: %d, %d", today, week)
	}
	tl.prune(now)
	if _, ok := tl[day(7).Format(dayFormat)]; ok {
		t.Error("an eighth day is kept")
	}
}

func cause1(t *testing.T, root string, d disk) cause {
	t.Helper()
	return probe{root: root}.cause(d)
}

var disk1 = disk{Name: "disk1", Device: "sdc"}

func TestAContainerReadingThroughAUserShareWokeTheDisk(t *testing.T) {
	root := unraid(t)
	id := strings.Repeat("ab", 32)
	process(t, root, proc{pid: 901, ppid: 1, comm: "shfs", cgroup: "0::/", fds: []string{"/mnt/disk1/Media/film.mkv"}})
	process(t, root, proc{pid: 2001, ppid: 2000, comm: "Plex Media Serv", cgroup: "0::/docker/" + id, fds: []string{"/dev/null", "/mnt/user/Media/film.mkv"}})
	write(t, root, "/var/lib/docker/containers/"+id+"/config.v2.json", `{"State":{"Running":true},"Name":"/Plex"}`)
	if got := cause1(t, root, disk1); got != (cause{By: "container", Who: "Plex"}) {
		t.Errorf("%+v", got)
	}
}

func TestAVMWithItsDiskImageOnTheDiskWokeIt(t *testing.T) {
	root := unraid(t)
	process(t, root, proc{pid: 3000, ppid: 1, comm: "qemu-system-x86", cgroup: "0::/machine/qemu-1",
		cmdline: []string{"/usr/bin/qemu-system-x86_64", "-name", "guest=Windows 11,debug-threads=on", "-S"},
		fds:     []string{"/mnt/disk1/domains/win/vdisk1.img"}})
	if got := cause1(t, root, disk1); got != (cause{By: "vm", Who: "Windows 11"}) {
		t.Errorf("%+v", got)
	}
}

func TestAnSMBClientIsNamedByItsAddress(t *testing.T) {
	root := unraid(t)
	process(t, root, proc{pid: 901, ppid: 1, comm: "shfs", cgroup: "0::/", fds: []string{"/mnt/disk1/Docs/a.pdf"}})
	process(t, root, proc{pid: 4000, ppid: 3999, comm: "smbd", cgroup: "0::/", fds: []string{"socket:[777]", "/mnt/user/Docs/a.pdf"}})
	write(t, root, "/proc/net/tcp", "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
		"   0: 0A0200C0:01BD 1E0200C0:C350 01 00000000:00000000 00:00000000 00000000     0        0 777 1 0000000000000000 20 4 30 10 -1\n")
	if got := cause1(t, root, disk1); got != (cause{By: "smb", Who: "192.0.2.30"}) {
		t.Errorf("%+v", got)
	}
}

func TestAnSMBClientWithoutAKnownAddressIsStillAnSMBClient(t *testing.T) {
	root := unraid(t)
	process(t, root, proc{pid: 4000, ppid: 3999, comm: "smbd", cgroup: "0::/", fds: []string{"/mnt/disk1/Docs/a.pdf"}})
	if got := cause1(t, root, disk1); got != (cause{By: "smb_any"}) {
		t.Errorf("%+v", got)
	}
}

func TestAProgramStartedByCronIsAScheduledTask(t *testing.T) {
	root := unraid(t)
	process(t, root, proc{pid: 1400, ppid: 1, comm: "crond", cgroup: "0::/"})
	process(t, root, proc{pid: 5999, ppid: 1400, comm: "sh", cgroup: "0::/"})
	process(t, root, proc{pid: 6000, ppid: 5999, comm: "rsync", cgroup: "0::/", fds: []string{"/mnt/disk1/Backup/x.tar"}})
	if got := cause1(t, root, disk1); got != (cause{By: "task", Who: "rsync"}) {
		t.Errorf("%+v", got)
	}
}

func TestAnyOtherProgramIsNamed(t *testing.T) {
	root := unraid(t)
	process(t, root, proc{pid: 7000, ppid: 1, comm: "find", cgroup: "0::/", fds: []string{"/mnt/disk1"}})
	if got := cause1(t, root, disk1); got != (cause{By: "process", Who: "find"}) {
		t.Errorf("%+v", got)
	}
}

func TestTheRunningMoverWokeTheDisk(t *testing.T) {
	root := unraid(t)
	process(t, root, proc{pid: 5000, ppid: 1, comm: "move", cgroup: "0::/"})
	write(t, root, "/var/run/mover.pid", "5000\n")
	if got := cause1(t, root, disk1); got != (cause{By: "mover"}) {
		t.Errorf("%+v", got)
	}
}

func TestAMoverPidFileOfAProcessThatIsGoneProvesNothing(t *testing.T) {
	root := unraid(t)
	write(t, root, "/var/run/mover.pid", "5000\n")
	if got := cause1(t, root, disk1); got != (cause{By: "unknown"}) {
		t.Errorf("%+v", got)
	}
}

func TestAParityCheckWakesTheArrayButNotAPool(t *testing.T) {
	root := unraid(t)
	write(t, root, "/var/local/emhttp/var.ini", "mdState=\"STARTED\"\nmdResyncPos=\"123456\"\n")
	if got := cause1(t, root, disk1); got != (cause{By: "parity"}) {
		t.Errorf("disk1: %+v", got)
	}
	if got := cause1(t, root, disk{Name: "backup", Device: "sdf"}); got != (cause{By: "unknown"}) {
		t.Errorf("backup: %+v", got)
	}
}

func TestAnNFSClientWithAFileOpenOnTheDiskIsNamedByItsAddress(t *testing.T) {
	root := unraid(t)
	process(t, root, proc{pid: 901, ppid: 1, comm: "shfs", cgroup: "0::/", fds: []string{"/mnt/disk1/Media/show.mkv"}})
	write(t, root, "/proc/fs/nfsd/clients/3/info", "clientid: 0x1\naddress: \"192.0.2.40:798\"\nname: \"Linux NFSv4.2 laptop\"\n")
	write(t, root, "/proc/fs/nfsd/clients/3/states", "- 0x0001: { type: open, access: r-, deny: --, superblock: \"00:2d:1234\", filename: \"show.mkv\", owner: \"open id:x\" }\n")
	if got := cause1(t, root, disk1); got != (cause{By: "nfs", Who: "192.0.2.40"}) {
		t.Errorf("%+v", got)
	}
}

func TestNothingOpenOnTheDiskLeavesTheCauseUnknown(t *testing.T) {
	root := unraid(t)
	process(t, root, proc{pid: 7000, ppid: 1, comm: "bash", cgroup: "0::/", fds: []string{"/mnt/disk10/x", "/mnt/user/Media/a.mkv", "/mnt/cache/appdata/db"}})
	if got := cause1(t, root, disk1); got != (cause{By: "unknown"}) {
		t.Errorf("%+v", got)
	}
}

func TestAPoolDiskIsFoundUnderItsPoolsMount(t *testing.T) {
	root := unraid(t)
	process(t, root, proc{pid: 7000, ppid: 1, comm: "restic", cgroup: "0::/", fds: []string{"/mnt/backup/repo/data/00/abc"}})
	if got := cause1(t, root, disk{Name: "backup2", Device: "sdg"}); got != (cause{By: "process", Who: "restic"}) {
		t.Errorf("%+v", got)
	}
}
