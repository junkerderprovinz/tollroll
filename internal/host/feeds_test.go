package host

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/junkerderprovinz/tollroll/internal/board"
)

func appendTo(t *testing.T, root, file string, lines ...string) {
	t.Helper()
	p := filepath.Join(root, file)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func dockerLine(at time.Time, text string) string {
	b, _ := json.Marshal(map[string]string{"log": text + "\n", "stream": "stdout", "time": at.Format(time.RFC3339Nano)})
	return string(b)
}

// of lists the lines of app in the order they were read.
func of(lines []board.Line, app string) []board.Line {
	var out []board.Line
	for _, l := range lines {
		if l.App == app {
			out = append(out, l)
		}
	}
	seq := func(l board.Line) int {
		n, _ := strconv.Atoi(l.ID[strings.LastIndex(l.ID, ":")+1:])
		return n
	}
	slices.SortFunc(out, func(a, b board.Line) int { return seq(a) - seq(b) })
	return out
}

func TestTheLogsOfRunningContainersAreASourceOfTheirOwn(t *testing.T) {
	s := newServer(t)
	plex, stopped := "aa"+strings64, "bb"+strings64
	container(t, s.root, plex, "Plex", `{}`)
	write(t, s.root, "/var/lib/docker/containers/"+stopped+"/config.v2.json", `{"Name":"/Old","State":{"Running":false},"LogPath":"/var/lib/docker/containers/`+stopped+`/`+stopped+`-json.log"}`)
	appendTo(t, s.root, "/var/lib/docker/containers/"+stopped+"/"+stopped+"-json.log", dockerLine(s.at, "ERROR: never read"))
	appendTo(t, s.root, "/var/lib/docker/containers/"+plex+"/"+plex+"-json.log",
		dockerLine(s.at.Add(-time.Minute), "Starting Plex Media Server"),
		dockerLine(s.at.Add(-time.Minute), "ERROR - Database is locked"),
		dockerLine(s.at.Add(-time.Minute), "[warn] Transcoder is slow"),
	)
	h := s.host(t)
	s.look(h)
	lines := of(h.Lines("en"), Containers)
	if len(lines) != 3 {
		t.Fatalf("%+v", lines)
	}
	levels := map[string]string{}
	for _, l := range lines {
		levels[l.Text] = l.Level
		if l.Text == "Plex: Starting Plex Media Server" && !l.Quiet {
			t.Errorf("an info line stays unfolded: %+v", l)
		}
	}
	if levels["Plex: ERROR - Database is locked"] != "error" || levels["Plex: [warn] Transcoder is slow"] != "warn" || levels["Plex: Starting Plex Media Server"] != "info" {
		t.Errorf("%v", levels)
	}
	if r := row(h.Rows("en"), Containers); r == nil || r.Name != "Containers" || !r.Local || r.State != "ok" {
		t.Errorf("%+v", r)
	}
	appendTo(t, s.root, "/var/lib/docker/containers/"+plex+"/"+plex+"-json.log", dockerLine(s.at, "Library scan done"))
	s.look(h)
	if n := len(of(h.Lines("en"), Containers)); n != 4 {
		t.Errorf("read on: %d lines", n)
	}
}

func TestOnlyTheEndOfABigLogIsRead(t *testing.T) {
	s := newServer(t)
	id := "cc" + strings64
	container(t, s.root, id, "Chatty", `{}`)
	var many []string
	for range 5000 {
		many = append(many, dockerLine(s.at, "some old chatter that nobody needs to see again"))
	}
	many = append(many, dockerLine(s.at, "the newest line"))
	appendTo(t, s.root, "/var/lib/docker/containers/"+id+"/"+id+"-json.log", many...)
	h := s.host(t)
	s.look(h)
	lines := of(h.Lines("en"), Containers)
	if len(lines) == 0 || len(lines) > keepFeed || lines[len(lines)-1].Text != "Chatty: the newest line" {
		t.Fatalf("%d lines", len(lines))
	}
}

func TestContainerLogsWaitWhileTheDiskDockerLivesOnSleeps(t *testing.T) {
	s := newServer(t)
	write(t, s.root, "/proc/self/mountinfo", mountinfo+"60 46 8:81 /system/docker /var/lib/docker rw,noatime - xfs /dev/sdf1 rw\n")
	id := "dd" + strings64
	container(t, s.root, id, "Plex", `{}`)
	appendTo(t, s.root, "/var/lib/docker/containers/"+id+"/"+id+"-json.log", dockerLine(s.at, "hello"))
	h := s.host(t)
	s.look(h)
	if lines := of(h.Lines("en"), Containers); len(lines) != 0 {
		t.Errorf("read from a sleeping disk: %+v", lines)
	}
}

func TestPluginLogsAndPluginLinesOfTheSyslogAreASourceOfTheirOwn(t *testing.T) {
	s := newServer(t)
	write(t, s.root, "/boot/config/plugins/unassigned.devices.plg", "<PLUGIN/>")
	write(t, s.root, "/boot/config/plugins/fix.common.problems.plg", "<PLUGIN/>")
	h := s.host(t)
	s.look(h)
	appendTo(t, s.root, "/var/log/unassigned.devices.log", "Mounting partition 'sdh1' at mountpoint '/mnt/disks/usb'...", "Error: device busy")
	appendTo(t, s.root, "/var/log/fix.common.problems/fcp.log", "scan started")
	appendTo(t, s.root, "/var/log/nginx/error.log", "not a plugin")
	s.log(t, "fix.common.problems: Warning: share cache is set to no")
	s.look(h)
	var got []string
	for _, l := range of(h.Lines("en"), Plugins) {
		got = append(got, l.Level+" "+l.Text)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"info unassigned.devices: Mounting partition", "warn unassigned.devices: Error: device busy",
		"warn fix.common.problems: Warning: share cache is set to no"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "not a plugin") || !strings.Contains(joined, "scan started") {
		t.Errorf("%s", joined)
	}
	if l := find(h.Lines("en"), "share cache is set to no"); l == nil || l.App != Plugins {
		t.Errorf("the syslog line stays with Unraid: %+v", l)
	}
}

func TestUserScriptLogsAreASourceOfTheirOwnByTheScriptsName(t *testing.T) {
	s := newServer(t)
	write(t, s.root, "/boot/config/plugins/user.scripts/scripts/backup_photos/name", "Backup photos\n")
	appendTo(t, s.root, "/tmp/user.scripts/tmpScripts/backup_photos/log.txt", "Script Starting Nov 17, 2026 11:00.01", "rsync: failed to set times")
	h := s.host(t)
	s.look(h)
	lines := of(h.Lines("en"), Scripts)
	if len(lines) != 2 || lines[0].Text != "Backup photos: Script Starting Nov 17, 2026 11:00.01" || lines[1].Level != "warn" {
		t.Fatalf("%+v", lines)
	}
	write(t, s.root, "/tmp/user.scripts/tmpScripts/backup_photos/log.txt", "Script Starting Nov 17, 2026 12:00.01\n")
	s.look(h)
	if lines := of(h.Lines("en"), Scripts); len(lines) != 3 || lines[2].Text != "Backup photos: Script Starting Nov 17, 2026 12:00.01" {
		t.Errorf("a new run: %+v", lines)
	}
	if r := row(h.Rows("en"), Scripts); r == nil || r.Name != "User Scripts" {
		t.Errorf("%+v", r)
	}
}
