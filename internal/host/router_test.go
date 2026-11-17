package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/junkerderprovinz/tollroll/internal/alert"
	"github.com/junkerderprovinz/tollroll/internal/board"
)

const rsyslogCfg = `local_server="192.168.1.10"
server_folder="/mnt/user/system/logs"
server_protocol="udp"
server_port="514"
server_filename="syslog-%FROMHOST-IP%.log"
remote_server="192.168.1.10"
`

// routerServer is a server whose syslog server keeps the log of the router
// at 192.168.1.1 on the cache pool, and its own log beside it.
func routerServer(t *testing.T) *server {
	s := newServer(t)
	write(t, s.root, "/boot/config/rsyslog.cfg", rsyslogCfg)
	write(t, s.root, "/mnt/cache/system/logs/syslog-192.168.1.1.log", "")
	write(t, s.root, "/mnt/cache/system/logs/syslog-192.168.1.10.log", "")
	return s
}

func (s *server) routerHost(t *testing.T) *Host {
	h := s.host(t)
	h.o.Own = func() []string { return []string{"192.168.1.10", "Tower"} }
	return h
}

func (s *server) router(t *testing.T, file, name string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(s.root, file), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, l := range lines {
		if _, err := f.WriteString(s.at.Format(time.Stamp) + " " + name + " " + l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

const gateway = "/mnt/cache/system/logs/syslog-192.168.1.1.log"

func needs(rows []board.App, key string) []string {
	var out []string
	if r := row(rows, key); r != nil {
		for _, n := range r.Needs {
			out = append(out, n.Text)
		}
	}
	return out
}

func TestRouterLinesGetARowOfTheirOwnInPlainWords(t *testing.T) {
	s := routerServer(t)
	s.router(t, gateway, "gw", "system,info,account user admin logged in from 192.168.1.20 via winbox")
	h := s.routerHost(t)
	s.look(h)
	s.router(t, gateway, "gw",
		"system,info,account user admin logged in from 192.168.1.20 via web",
		"system,info,account user admin logged in from 203.0.113.9 via ssh",
	)
	s.look(h)

	rows := h.Rows("en")
	r := row(rows, Router)
	if r == nil || r.Name != "Router" || !strings.Contains(r.Note, "gw") {
		t.Fatalf("router row: %+v", r)
	}
	en := alert.LoadTexts(langDir, "en")
	want := strings.NewReplacer("{user}", "admin", "{router}", "gw", "{addr}", "203.0.113.9").Replace(en["h_r_new_login"])
	if got := needs(rows, Router); len(got) != 1 || got[0] != want {
		t.Errorf("needs %q, want %q", got, want)
	}
	var known *board.Line
	for _, l := range h.Lines("en") {
		if l.App == Router && strings.Contains(l.Raw, "via web") {
			known = &l
		}
	}
	if known == nil || !known.Quiet || known.Raw != "gw: system,info,account user admin logged in from 192.168.1.20 via web" {
		t.Errorf("a sign-in from a known address: %+v", known)
	}
	if got := needs(rows, Unraid); len(got) != 0 {
		t.Errorf("the server's row took the router's needs: %q", got)
	}
}

func TestTheServersOwnLogComingBackIsNoRouter(t *testing.T) {
	s := routerServer(t)
	s.router(t, "/mnt/cache/system/logs/syslog-192.168.1.10.log", "Tower", "kernel: md: disk2 read error, sector=1")
	h := s.routerHost(t)
	s.look(h)
	if got := h.RouterLog(); !got.Server || len(got.Senders) != 1 || got.Senders[0].Name != "192.168.1.1" {
		t.Errorf("%+v", got)
	}
}

func TestANewDeviceOnTheNetworkIsFlaggedOnce(t *testing.T) {
	s := routerServer(t)
	s.router(t, gateway, "gw", "dnsmasq-dhcp[812]: DHCPACK(br-lan) 192.168.1.50 aa:bb:cc:dd:ee:01 laptop")
	h := s.routerHost(t)
	s.look(h)
	s.router(t, gateway, "gw",
		"dnsmasq-dhcp[812]: DHCPACK(br-lan) 192.168.1.50 aa:bb:cc:dd:ee:01 laptop",
		"dhcp,info defconf assigned 192.168.1.77 for AA:BB:CC:DD:EE:02 phone",
		"dnsmasq-dhcp[812]: DHCPACK(br-lan) 192.168.1.77 aa:bb:cc:dd:ee:02 phone",
	)
	s.look(h)
	en := alert.LoadTexts(langDir, "en")
	want := strings.NewReplacer("{device}", "phone (aa:bb:cc:dd:ee:02)", "{ip}", "192.168.1.77").Replace(en["h_r_new_device"])
	if got := needs(h.Rows("en"), Router); len(got) != 1 || got[0] != want {
		t.Errorf("needs %q, want %q", got, want)
	}
}

func TestChangedForwardsRulesAndSettingsAreFlagged(t *testing.T) {
	s := routerServer(t)
	h := s.routerHost(t)
	s.look(h)
	s.router(t, gateway, "gw",
		"system,info nat rule added by winbox-3.41/tcp-msg(winbox):admin@192.168.1.20 (/ip firewall nat add chain=dstnat dst-port=8443)",
		"system,info filter rule changed by admin",
	)
	s.router(t, gateway, "console",
		"CEF:0|Vendor|Network|9.0.1|546|Admin Made Config Changes|1|UNIFIcategory=System UNIFIsubCategory=Admin UNIFIadmin=admin src=192.168.1.20 msg=admin made changes to Port Forwarding",
	)
	s.look(h)
	en := alert.LoadTexts(langDir, "en")
	got := strings.Join(needs(h.Rows("en"), Router), "\n")
	for _, k := range []string{"h_r_nat", "h_r_filter", "h_r_config"} {
		router := "gw"
		if k == "h_r_config" {
			router = "console"
		}
		if want := strings.ReplaceAll(en[k], "{router}", router); !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestAnIntrusionIsFlaggedOnceWhileItGoesOn(t *testing.T) {
	s := routerServer(t)
	h := s.routerHost(t)
	s.look(h)
	ids := "suricata[2211]: [1:2010935:3] ET SCAN Suspicious inbound to MSSQL port 1433 [Classification: Potentially Bad Traffic] [Priority: 2] {TCP} 198.51.100.7:51234 -> 192.168.1.5:1433"
	s.router(t, gateway, "gw", ids, ids)
	s.look(h)
	s.router(t, gateway, "gw", ids)
	s.look(h)
	got := needs(h.Rows("en"), Router)
	if len(got) != 1 || !strings.Contains(got[0], "ET SCAN Suspicious inbound to MSSQL port 1433") || !strings.Contains(got[0], "198.51.100.7:51234") {
		t.Errorf("%q", got)
	}
	n := 0
	for _, l := range h.Lines("en") {
		if l.App == Router && strings.Contains(l.Raw, "ET SCAN") {
			n++
		}
	}
	if n != 3 {
		t.Errorf("%d lines, want every one", n)
	}
}

func TestARouterThatFallsSilentIsFlaggedAndItsReturnToo(t *testing.T) {
	s := routerServer(t)
	s.router(t, gateway, "gw", "dhcp,info defconf assigned 192.168.1.50 for AA:BB:CC:DD:EE:01")
	h := s.routerHost(t)
	s.look(h)
	s.at = s.at.Add(7 * time.Hour)
	s.look(h)
	en := alert.LoadTexts(langDir, "en")
	want := strings.NewReplacer("{router}", "gw", "{h}", "6").Replace(en["h_r_silent"])
	if got := needs(h.Rows("en"), Router); len(got) != 1 || got[0] != want {
		t.Fatalf("needs %q, want %q", got, want)
	}
	s.router(t, gateway, "gw", "dhcp,info defconf assigned 192.168.1.50 for AA:BB:CC:DD:EE:01")
	s.look(h)
	if got := needs(h.Rows("en"), Router); len(got) != 0 {
		t.Errorf("still flagged: %q", got)
	}
	back := strings.ReplaceAll(en["h_r_back"], "{router}", "gw")
	found := false
	for _, l := range h.Lines("en") {
		found = found || l.Text == back
	}
	if !found {
		t.Errorf("no line %q", back)
	}
}

func TestARouterLogOnASpunDownDiskIsNotRead(t *testing.T) {
	s := routerServer(t)
	write(t, s.root, "/mnt/backup/system/logs/syslog-192.168.1.2.log", "")
	s.router(t, "/mnt/backup/system/logs/syslog-192.168.1.2.log", "ap", "system,info,account user admin logged in from 192.168.1.20 via web")
	h := s.routerHost(t)
	s.look(h)
	for _, sd := range h.RouterLog().Senders {
		if sd.Name == "ap" || sd.Name == "192.168.1.2" {
			t.Errorf("read a log on a sleeping disk: %+v", sd)
		}
	}
}

func TestTheRouterLogStaysOffWithoutUnraidsSyslogServer(t *testing.T) {
	s := routerServer(t)
	write(t, s.root, "/boot/config/rsyslog.cfg", `local_server=""`+"\n"+`server_folder="/mnt/user/system/logs"`+"\n")
	h := s.routerHost(t)
	s.look(h)
	if got := h.RouterLog(); got.Server || len(got.Senders) != 0 || row(h.Rows("en"), Router) != nil {
		t.Errorf("%+v", got)
	}
}

func TestRouterMessagesAreRead(t *testing.T) {
	for _, c := range []struct {
		msg, kind string
		args      map[string]string
	}{
		{"system,error,critical login failure for user admin from 198.51.100.4 via ssh", "fail", map[string]string{"user": "admin", "addr": "198.51.100.4"}},
		{"dropbear[1404]: Password auth succeeded for 'root' from 192.168.1.20:51515", "login", map[string]string{"user": "root", "addr": "192.168.1.20"}},
		{"dropbear[1404]: Bad password attempt for 'root' from 198.51.100.4:4242", "fail", map[string]string{"user": "root", "addr": "198.51.100.4"}},
		{"uhttpd[2]: luci: accepted login on / for root from 192.168.1.20", "login", map[string]string{"user": "root", "addr": "192.168.1.20"}},
		{"dhcpd: DHCPACK on 192.168.1.60 to 00:11:22:33:44:55 (printer) via igb1", "lease", map[string]string{"ip": "192.168.1.60", "mac": "00:11:22:33:44:55", "device": "printer (00:11:22:33:44:55)"}},
		{"CEF:0|Vendor|Network|9.0.1|544|Admin Accessed Network|1|UNIFIcategory=System UNIFIadmin=jo src=192.168.1.21 msg=jo accessed the network using the web", "login", map[string]string{"user": "jo", "addr": "192.168.1.21"}},
		{"CEF:0|Vendor|Network|9.0.1|400|WiFi Client Connected|1|UNIFIcategory=Monitoring UNIFIclientAlias=tablet UNIFIclientMac=AA:00:00:00:00:09 UNIFIclientIp=192.168.1.99", "lease", map[string]string{"mac": "aa:00:00:00:00:09", "ip": "192.168.1.99", "device": "tablet (aa:00:00:00:00:09)"}},
		{"CEF:0|Vendor|Network|9.0.1|700|Threat Detected and Blocked|7|UNIFIcategory=Security src=198.51.100.8 dst=192.168.1.5 msg=ET EXPLOIT probe", "ids", map[string]string{"sig": "ET EXPLOIT probe", "src": "198.51.100.8", "dst": "192.168.1.5"}},
		{"CEF:0|Vendor|Network|9.0.1|401|WiFi Client Disconnected|1|UNIFIclientMac=aa:00:00:00:00:09", "", nil},
		{"wireless,info 11:22:33:44:55:66@wlan1: connected, signal strength -60", "", nil},
	} {
		got := classifyRouter(c.msg)
		if got.kind != c.kind {
			t.Errorf("%s: kind %q, want %q", c.msg, got.kind, c.kind)
			continue
		}
		if c.kind == "" && !got.Quiet {
			t.Errorf("%s: not folded away", c.msg)
		}
		for k, v := range c.args {
			if got.Args[k] != v {
				t.Errorf("%s: %s=%q, want %q", c.msg, k, got.Args[k], v)
			}
		}
	}
}
