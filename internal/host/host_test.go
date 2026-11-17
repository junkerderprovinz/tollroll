package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/notify"

	"github.com/junkerderprovinz/tollroll/internal/alert"
	"github.com/junkerderprovinz/tollroll/internal/board"
)

const langDir = "../../plugin/src/tollroll/usr/local/emhttp/plugins/tollroll/lang"

type server struct {
	root  string
	at    time.Time
	used  map[string]float64
	files map[string]uint64

	mu   sync.Mutex
	sent []notify.Event
}

func newServer(t *testing.T) *server {
	root := unraid(t)
	write(t, root, "/etc/unraid-version", "version=\"7.3.4\"\n")
	write(t, root, "/var/log/syslog", "")
	return &server{root: root, at: now, used: map[string]float64{"/var/log": 0.3}}
}

func (s *server) host(t *testing.T) *Host {
	return New(Options{
		Root:  s.root,
		State: filepath.Join(s.root, "/boot/config/plugins/tollroll/watch.json"),
		Texts: func(lang string) map[string]string { return alert.LoadTexts(langDir, lang) },
		Now:   func() time.Time { return s.at },
		Space: func(path string) (Space, error) {
			rel := strings.TrimPrefix(path, s.root)
			return Space{Total: 1000, Free: uint64(1000 * (1 - s.used[rel])), Files: 100000, FilesFree: 100000 - s.files[rel]}, nil
		},
		Notify: func(_ context.Context, e notify.Event) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.sent = append(s.sent, e)
		},
	})
}

func (s *server) log(t *testing.T, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(s.root, "/var/log/syslog"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, l := range lines {
		if _, err := f.WriteString(s.at.Format(time.Stamp) + " Tower " + l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

// look lets five minutes pass and has h look once.
func (s *server) look(h *Host) {
	s.at = s.at.Add(5 * time.Minute)
	h.step(context.Background())
}

func find(lines []board.Line, raw string) *board.Line {
	for i := range lines {
		if strings.Contains(lines[i].Raw, raw) {
			return &lines[i]
		}
	}
	return nil
}

func row(rows []board.App, key string) *board.App {
	for i := range rows {
		if rows[i].Key == key {
			return &rows[i]
		}
	}
	return nil
}

func TestSyslogLinesReachTheLogInTheReadersLanguage(t *testing.T) {
	s := newServer(t)
	s.log(t,
		"kernel: md: disk2 read error, sector=1953525160",
		"avahi-daemon[3001]: Joining mDNS multicast group on interface veth12ab.IPv6 with address fe80::1.",
		"someprog[12]: something happened",
	)
	h := s.host(t)
	s.look(h)
	lines := h.Lines("de")
	md := find(lines, "md: disk2 read error")
	if md == nil || md.Level != "error" || md.App != "unraid" || md.Quiet || md.Raw != "kernel: md: disk2 read error, sector=1953525160" {
		t.Fatalf("%+v", md)
	}
	de := alert.LoadTexts(langDir, "de")
	if want := strings.ReplaceAll(de["h_s_md_read"], "{disk}", "Disk 2"); md.Text != want {
		t.Errorf("text %q, want %q", md.Text, want)
	}
	if l := find(lines, "avahi-daemon"); l == nil || !l.Quiet {
		t.Errorf("noise: %+v", l)
	}
	if l := find(lines, "someprog"); l == nil || l.Text != "someprog: something happened" || l.Quiet {
		t.Errorf("unknown: %+v", l)
	}
}

func TestADriveNamedByItsDeviceIsNamedLikeUnraidToo(t *testing.T) {
	s := newServer(t)
	s.log(t, "kernel: I/O error, dev sdc, sector 1953525160 op 0x0:(READ)")
	h := s.host(t)
	s.look(h)
	if l := find(h.Lines("en"), "I/O error"); l == nil || !strings.Contains(l.Text, "Disk 1 (sdc)") {
		t.Errorf("%+v", l)
	}
}

func TestAnErrorOfTheLastDayShowsOnTheUnraidRow(t *testing.T) {
	s := newServer(t)
	h := s.host(t)
	s.look(h)
	if r := row(h.Rows("en"), "unraid"); r == nil || r.State != "ok" || r.Need != nil || r.Version != "7.3.4" || !r.Local {
		t.Fatalf("a quiet server: %+v", r)
	}
	s.log(t, "kernel: XFS (md3p1): Metadata corruption detected at xfs_dinode_verify+0xa0/0x732 [xfs]")
	s.look(h)
	r := row(h.Rows("en"), "unraid")
	if r.State != "warning" || r.Need == nil || r.Need.Severity != "error" || !strings.Contains(r.Need.Text, "Disk 3") {
		t.Fatalf("%+v", r)
	}
	s.at = s.at.Add(25 * time.Hour)
	s.look(h)
	if r := row(h.Rows("en"), "unraid"); r.Need != nil {
		t.Errorf("a day later: %+v", r.Need)
	}
}

func TestASignInFromAKnownAddressIsARoutineLine(t *testing.T) {
	s := newServer(t)
	s.log(t, "webgui: Successful login user root from 192.0.2.20")
	h := s.host(t)
	s.look(h)
	if l := find(h.Lines("en"), "192.0.2.20"); l == nil || !l.Quiet {
		t.Fatalf("a sign-in before TollRoll watched: %+v", l)
	}
	s.log(t, "sshd-session[2201]: Accepted publickey for root from 198.51.100.9 port 50122 ssh2: ED25519 SHA256:abc")
	s.look(h)
	lines := h.Lines("en")
	if l := find(lines, "198.51.100.9 port"); l == nil || l.Quiet {
		t.Errorf("a sign-in from a new address is routine: %+v", l)
	}
	var watch *board.Line
	for i := range lines {
		if strings.Contains(lines[i].Text, "has not seen before") {
			watch = &lines[i]
		}
	}
	if watch == nil || watch.Level != "warn" || !strings.Contains(watch.Text, "198.51.100.9") {
		t.Fatalf("no watch line: %+v", lines)
	}
	if r := row(h.Rows("en"), "unraid"); r.Need == nil || r.Need.Text != watch.Text {
		t.Errorf("row: %+v", r.Need)
	}
}

func TestAWakeIsLoggedWithItsCauseAndCountedOnTheDisksRow(t *testing.T) {
	s := newServer(t)
	h := s.host(t)
	s.look(h)
	r := row(h.Rows("en"), "disks")
	if r == nil || r.Note != "No disk woke up today" || len(r.Disks) != 3 {
		t.Fatalf("%+v", r)
	}
	process(t, s.root, proc{pid: 7000, ppid: 1, comm: "find", cgroup: "0::/", fds: []string{"/mnt/backup/x"}})
	s.log(t, "emhttpd: read SMART /dev/sdf")
	s.look(h)
	var woke *board.Line
	for _, l := range h.Lines("en") {
		if l.App == "disks" {
			woke = &l
		}
	}
	if woke == nil || woke.Text != "Backup woke up. Cause: the program find." || woke.Level != "info" {
		t.Fatalf("%+v", woke)
	}
	r = row(h.Rows("en"), "disks")
	if r.Note != "Wake-ups today: 1" {
		t.Errorf("note %q", r.Note)
	}
	for _, d := range r.Disks {
		if d.Name == "Backup" && (d.Today != 1 || d.Week != 1 || d.Last != "the program find") {
			t.Errorf("%+v", d)
		}
	}
}

func TestWakeCountsAndWhatWasLearnedSurviveARestart(t *testing.T) {
	s := newServer(t)
	s.log(t, "webgui: Successful login user root from 192.0.2.20")
	h := s.host(t)
	s.look(h)
	s.log(t, "emhttpd: read SMART /dev/sdb")
	s.look(h)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.Run(ctx)

	h2 := s.host(t)
	s.look(h2)
	for _, d := range row(h2.Rows("en"), "disks").Disks {
		if d.Name == "Parity" && d.Today != 1 {
			t.Errorf("%+v", d)
		}
	}
	s.log(t, "webgui: Successful login user root from 192.0.2.20")
	s.look(h2)
	for _, l := range h2.Lines("en") {
		if strings.Contains(l.Text, "has not seen before") {
			t.Errorf("a known address after a restart: %s", l.Text)
		}
	}
}

func TestVarLogAtNinetyPercentSendsOneUnraidNotification(t *testing.T) {
	s := newServer(t)
	write(t, s.root, "/var/log/samba/log.smbd", string(make([]byte, 4096)))
	process(t, s.root, proc{pid: 8100, ppid: 1, comm: "smbd", cgroup: "0::/", fds: []string{"/var/log/samba/log.smbd"}})
	h := s.host(t)
	s.look(h)
	s.used["/var/log"] = 0.82
	s.look(h)
	r := row(h.Rows("en"), "unraid")
	if r.Need == nil || r.Need.Text != "/var/log is 82% full. The biggest file is /var/log/samba/log.smbd (4 KB), written by the program smbd." {
		t.Fatalf("%+v", r.Need)
	}
	if len(s.sent) != 0 {
		t.Fatalf("notified at 82%%: %+v", s.sent)
	}
	s.used["/var/log"] = 0.91
	s.look(h)
	s.look(h)
	if len(s.sent) != 1 || s.sent[0].Title != "/var/log is almost full" || s.sent[0].Severity != notify.SeverityWarning {
		t.Fatalf("%+v", s.sent)
	}
	s.used["/var/log"] = 0.5
	s.look(h)
	if r := row(h.Rows("en"), "unraid"); r.Need != nil {
		t.Errorf("room again: %+v", r.Need)
	}
}

func TestContainersAndVMsThatStartOrStopAreLogged(t *testing.T) {
	s := newServer(t)
	id := "ef" + strings64
	write(t, s.root, "/var/lib/docker/containers/"+id+"/config.v2.json", `{"State":{"Running":false},"Name":"/Plex"}`)
	h := s.host(t)
	s.look(h)
	write(t, s.root, "/var/lib/docker/containers/"+id+"/config.v2.json", `{"State":{"Running":true},"Name":"/Plex"}`)
	future := s.at.Add(time.Hour)
	_ = os.Chtimes(filepath.Join(s.root, "/var/lib/docker/containers/"+id+"/config.v2.json"), future, future)
	write(t, s.root, "/var/run/libvirt/qemu/Windows 11.pid", "3000\n")
	write(t, s.root, "/var/run/libvirt/qemu/Windows 11.xml", "<domstatus/>")
	write(t, s.root, "/var/run/libvirt/qemu/driver.pid", "1\n")
	s.look(h)
	var got []string
	for _, l := range h.Lines("en") {
		got = append(got, l.Text)
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "The container Plex started.") || !strings.Contains(joined, "The VM Windows 11 started.") || strings.Contains(joined, "driver") {
		t.Errorf("%s", joined)
	}
}

func TestOffUnraidThereIsNothingToShow(t *testing.T) {
	h := New(Options{Root: t.TempDir(), Texts: func(string) map[string]string { return nil }})
	h.step(context.Background())
	if len(h.Rows("en")) != 0 || len(h.Lines("en")) != 0 {
		t.Error("rows off Unraid")
	}
}
