package host

import (
	"maps"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/status"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

func TestALineIsSplitIntoTimeProgramAndMessage(t *testing.T) {
	l, ok := parse("Nov 17 10:02:33 Tower sshd-session[2201]: Accepted publickey for root from 192.0.2.20 port 50122 ssh2", now)
	if !ok {
		t.Fatal("not parsed")
	}
	if !l.At.Equal(time.Date(2026, 11, 17, 10, 2, 33, 0, time.UTC)) || l.Prog != "sshd-session" || l.Msg != "Accepted publickey for root from 192.0.2.20 port 50122 ssh2" {
		t.Errorf("%+v", l)
	}
}

func TestALineFromLastDecemberBelongsToLastYear(t *testing.T) {
	jan := time.Date(2027, 1, 2, 9, 0, 0, 0, time.UTC)
	l, ok := parse("Dec 31 23:59:58 Tower kernel: md: sync done. time=63421sec", jan)
	if !ok || l.At.Year() != 2026 {
		t.Errorf("%+v", l)
	}
}

func TestAnRFC3339TimestampIsRead(t *testing.T) {
	l, ok := parse("2026-11-17T10:02:33.123456+01:00 Tower webgui: Successful login user root from 192.0.2.20", now)
	if !ok || !l.At.Equal(time.Date(2026, 11, 17, 9, 2, 33, 123456000, time.UTC)) || l.Prog != "webgui" {
		t.Errorf("%+v", l)
	}
}

func TestAGarbledLineIsNotParsed(t *testing.T) {
	for _, s := range []string{"", "hello", "Nov 17 xx:yy:zz Tower kernel: x"} {
		if _, ok := parse(s, now); ok {
			t.Errorf("%q parsed", s)
		}
	}
}

func TestTheCommonLinesGetAPlainSentence(t *testing.T) {
	cases := []struct {
		line  string
		level status.Severity
		key   string
		args  map[string]string
	}{
		{"kernel: md: disk2 read error, sector=1953525160", status.SeverityError, "h_s_md_read", map[string]string{"disk": "Disk 2"}},
		{"kernel: md: disk1 write error, sector=88", status.SeverityError, "h_s_md_write", map[string]string{"disk": "Disk 1"}},
		{"kernel: I/O error, dev sdc, sector 1953525160 op 0x0:(READ) flags 0x0 phys_seg 1 prio class 2", status.SeverityError, "h_s_io", map[string]string{"dev": "sdc"}},
		{"kernel: blk_update_request: I/O error, dev sdd, sector 2048", status.SeverityError, "h_s_io", map[string]string{"dev": "sdd"}},
		{"kernel: ata5: hard resetting link", status.SeverityWarn, "h_s_link_reset", map[string]string{"port": "ata5"}},
		{"root: Warning [TOWER] - current pending sector is 8", status.SeverityWarn, "h_s_smart", nil},
		{"emhttpd: Alert [TOWER] - Disk 3 reallocated sector ct is 24", status.SeverityWarn, "h_s_smart", nil},
		{"kernel: mdcmd (36): start STOPPED", status.SeverityInfo, "h_s_array_start", nil},
		{"kernel: mdcmd (51): stop", status.SeverityInfo, "h_s_array_stop", nil},
		{"kernel: mdcmd (40): check nocorrect", status.SeverityInfo, "h_s_parity_start", nil},
		{"kernel: md: sync done. time=63421sec", status.SeverityInfo, "h_s_parity_done", nil},
		{"kernel: md: recovery thread: P incorrect, sector=1234", status.SeverityWarn, "h_s_parity_err", nil},
		{"move: mover: started", status.SeverityInfo, "h_s_mover_start", nil},
		{"root: mover: finished", status.SeverityInfo, "h_s_mover_done", nil},
		{"rc.docker: Plex: started successfully!", status.SeverityInfo, "h_s_container_up", map[string]string{"name": "Plex"}},
		{"rc.docker: Starting dockerd ...", status.SeverityInfo, "h_s_docker_up", nil},
		{"rc.docker: Stopping dockerd ...", status.SeverityInfo, "h_s_docker_down", nil},
		{"kernel: igb 0000:03:00.0 eth0: igb: eth0 NIC Link is Up 1000 Mbps Full Duplex, Flow Control: RX/TX", status.SeverityInfo, "h_s_link_up", map[string]string{"port": "eth0", "speed": "1000 Mbps"}},
		{"kernel: r8169 0000:04:00.0 eth1: Link is Up - 2.5Gbps/Full - flow control rx/tx", status.SeverityInfo, "h_s_link_up", map[string]string{"port": "eth1", "speed": "2.5Gbps"}},
		{"kernel: e1000e 0000:00:19.0 eth0: NIC Link is Down", status.SeverityWarn, "h_s_link_down", map[string]string{"port": "eth0"}},
		{"kernel: bond0: (slave eth0): link status definitely down, disabling slave", status.SeverityWarn, "h_s_link_down", map[string]string{"port": "eth0"}},
		{"webgui: Successful login user root from 192.0.2.20", status.SeverityInfo, "h_s_web_ok", map[string]string{"user": "root", "addr": "192.0.2.20"}},
		{"webGUI: Unsuccessful login user admin from 198.51.100.7", status.SeverityWarn, "h_s_web_fail", map[string]string{"user": "admin", "addr": "198.51.100.7"}},
		{"sshd-session[2201]: Accepted publickey for root from 192.0.2.20 port 50122 ssh2: ED25519 SHA256:abc", status.SeverityInfo, "h_s_ssh_ok", map[string]string{"user": "root", "addr": "192.0.2.20"}},
		{"sshd[2210]: Accepted password for jo from 2001:db8::5 port 41000 ssh2", status.SeverityInfo, "h_s_ssh_ok", map[string]string{"user": "jo", "addr": "2001:db8::5"}},
		{"sshd[2210]: Failed password for root from 198.51.100.7 port 41000 ssh2", status.SeverityWarn, "h_s_ssh_fail", map[string]string{"user": "root", "addr": "198.51.100.7"}},
		{"sshd-session[2211]: Failed password for invalid user admin from 198.51.100.7 port 41002 ssh2", status.SeverityWarn, "h_s_ssh_fail", map[string]string{"user": "admin", "addr": "198.51.100.7"}},
		{"kernel: Out of memory: Killed process 4242 (java) total-vm:8123456kB, anon-rss:4000kB", status.SeverityError, "h_s_oom", map[string]string{"proc": "java"}},
		{"kernel: Memory cgroup out of memory: Killed process 5151 (node) total-vm:1420048kB, anon-rss:97128kB", status.SeverityWarn, "h_s_oom_cg", map[string]string{"proc": "node"}},
		{"kernel: XFS (md1p1): Metadata corruption detected at xfs_dinode_verify+0xa0/0x732 [xfs], inode 0x1234 dinode", status.SeverityError, "h_s_fs", map[string]string{"disk": "Disk 1"}},
		{"kernel: XFS (md3p1): Corruption of in-memory data (0x8) detected at xfs_trans_cancel+0x1a/0x40. Shutting down filesystem.", status.SeverityError, "h_s_fs_off", map[string]string{"disk": "Disk 3"}},
		{"kernel: BTRFS error (device nvme0n1p1): bdev /dev/nvme0n1p1 errs: wr 0, rd 0, flush 0, corrupt 3, gen 0", status.SeverityError, "h_s_fs", map[string]string{"disk": "nvme0n1p1"}},
		{"kernel: EXT4-fs error (device sdb1): ext4_lookup:1700: inode #2: comm ls: deleted inode referenced", status.SeverityError, "h_s_fs", map[string]string{"disk": "sdb1"}},
	}
	for _, c := range cases {
		l, ok := parse("Nov 17 10:00:00 Tower "+c.line, now)
		if !ok {
			t.Errorf("%q not parsed", c.line)
			continue
		}
		got := classify(l)
		if got.Level != c.level || got.Key != c.key || got.Quiet || !sameArgs(got.Args, c.args) {
			t.Errorf("%q\n got %+v\nwant %s %s %v", c.line, got, c.level, c.key, c.args)
		}
	}
}

func sameArgs(a, b map[string]string) bool {
	return len(a) == len(b) && (len(a) == 0 || maps.Equal(a, b))
}

func TestKnownNoiseIsQuiet(t *testing.T) {
	for _, line := range []string{
		"kernel: docker0: port 2(veth9f01a2b) entered disabled state",
		"kernel: br0: port 1(eth0) entered forwarding state",
		"kernel: br-1a2b3c4d5e6f: port 3(veth77aa) entered blocking state",
		"kernel: veth12ab34c: renamed from eth0",
		"kernel: eth0: renamed from veth12ab34c",
		"kernel: veth12ab34c (unregistering): left promiscuous mode",
		"kernel: vethd00d: entered allmulticast mode",
		"kernel: IPv6: ADDRCONF(NETDEV_CHANGE): veth12ab: link becomes ready",
		"kernel: elogind-daemon[1500]: New session 42 of user root.",
		"elogind-daemon[1500]: Removed session 42.",
		"kernel: oom_kill_process+0x82/0x190",
		"kernel: [  pid  ]   uid  tgid total_vm      rss name",
		"kernel: BTRFS info (device loop2): using free space tree",
		"avahi-daemon[3001]: Joining mDNS multicast group on interface veth12ab.IPv6 with address fe80::1.",
		"sshd-session[2300]: Connection closed by authenticating user root 198.51.100.7 port 41010 [preauth]",
		"sshd-session[2300]: pam_unix(sshd:session): session opened for user root(uid=0) by (uid=0)",
		"sshd-session[2300]: Accepted key ED25519 SHA256:abc found at /root/.ssh/authorized_keys:5",
		"sshd-session[2300]: Received disconnect from 192.0.2.20 port 63930:11: disconnected by user",
		"smbd[910]:   create_canon_ace_lists: unable to map SID S-1-5-21-1 to uid or gid.",
		"smbd[910]: [2026/11/17 10:00:00.123,  0] ../../source3/smbd/posix_acls.c:1234(create_canon_ace_lists)",
		"emhttpd: read SMART /dev/sdc",
		"move: file /mnt/cache/Media/a.mkv",
		"crond[3081]: exit status 1 from user root /usr/local/sbin/mover &> /dev/null",
		"monitor_nchan: Stop running nchan processes",
		"rsyslogd: [origin software=\"rsyslogd\"] rsyslogd was HUPed",
		"ntpd[1700]: kernel reports TIME_ERROR: 0x41: Clock Unsynchronized",
	} {
		l, ok := parse("Nov 17 10:00:00 Tower "+line, now)
		if !ok {
			t.Errorf("%q not parsed", line)
			continue
		}
		if got := classify(l); !got.Quiet || got.Level != status.SeverityInfo {
			t.Errorf("%q: %+v", line, got)
		}
	}
}

func TestASpinDownIsQuietButSaysSo(t *testing.T) {
	l, _ := parse("Nov 17 05:55:02 Tower emhttpd: spinning down /dev/sde", now)
	got := classify(l)
	if !got.Quiet || got.Key != "h_s_spun_down" || got.Args["dev"] != "sde" {
		t.Errorf("%+v", got)
	}
}

func TestAnUnknownLineKeepsItsWordsAndGuessesTheLevelFromThem(t *testing.T) {
	for line, level := range map[string]status.Severity{
		"someprog[12]: something happened":                                    status.SeverityInfo,
		"nginx: 2026/11/17 10:00:00 [error] 1234#1234: upstream timed out":    status.SeverityWarn,
		"kernel: usb 1-4: device descriptor read/64, error -71":               status.SeverityWarn,
		"kernel: Kernel panic - not syncing: Fatal exception in interrupt":    status.SeverityError,
		"kernel: BUG: unable to handle page fault for address: ffffb1c2c0000": status.SeverityError,
	} {
		l, _ := parse("Nov 17 10:00:00 Tower "+line, now)
		got := classify(l)
		if got.Key != "" || got.Quiet || got.Level != level {
			t.Errorf("%q: %+v, want level %s", line, got, level)
		}
	}
}

func TestDiskNamesReadAsUnraidShowsThem(t *testing.T) {
	for name, want := range map[string]string{
		"disk1": "Disk 1", "disk12": "Disk 12", "parity": "Parity", "parity2": "Parity 2",
		"cache": "Cache", "fast2": "Fast 2", "md4p1": "Disk 4", "md4": "Disk 4", "sdb1": "sdb1",
	} {
		if got := diskLabel(name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}
