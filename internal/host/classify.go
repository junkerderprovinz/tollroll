package host

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/junkerderprovinz/parleyport/kit/status"
)

// class is what a syslog line means: how bad it is, whether it is routine
// noise, and the plain sentence that says it, by its key in the language
// files with the values it fills in. An empty Key keeps the line's own words.
type class struct {
	Level status.Severity
	Quiet bool
	Key   string
	Args  map[string]string
}

// rule matches a line by its program and message. Names lists what the
// message's groups fill in, a group named disk read as Unraid names disks.
type rule struct {
	prog  *regexp.Regexp
	msg   *regexp.Regexp
	level status.Severity
	quiet bool
	key   string
	names []string
}

func r(prog, msg string, level status.Severity, key string, names ...string) rule {
	return rule{prog: regexp.MustCompile(`^(?i:` + prog + `)$`), msg: regexp.MustCompile(msg), level: level, key: key, names: names}
}

func noise(prog, msg string) rule { return quiet(r(prog, msg, info, "")) }

// quiet marks what a rule matches as routine, folded away in the log.
func quiet(ru rule) rule {
	ru.quiet = true
	return ru
}

const (
	info  = status.SeverityInfo
	warn  = status.SeverityWarn
	fault = status.SeverityError
)

const (
	anyProg = `[^:]+`
	sshd    = `sshd(-session)?`
	// iface is a network port a person plugged a cable into, not one of the
	// virtual ones Docker and the VMs come and go with.
	iface = `(eth\d+|bond\d+|en[a-z0-9]+)`
)

// rules are tried in order and the first match wins, so the specific ones
// come before the noise that would also match them.
var rules = []rule{
	r("kernel", `^md: (disk\d+) read error`, fault, "h_s_md_read", "disk"),
	r("kernel", `^md: (disk\d+) write error`, fault, "h_s_md_write", "disk"),
	r("kernel", `I/O error, dev (\w+), sector`, fault, "h_s_io", "dev"),
	r("kernel", `^(ata\d+)(?:\.\d+)?: hard resetting link`, warn, "h_s_link_reset", "port"),
	r(anyProg, `(?i)(current pending sector|reallocated sector|offline uncorrectable|reported uncorrect|udma crc error|smart overall-health|failing now)`, warn, "h_s_smart"),
	r("kernel", `^mdcmd \(\d+\): start`, info, "h_s_array_start"),
	r("kernel", `^mdcmd \(\d+\): stop`, info, "h_s_array_stop"),
	r("emhttpd", `^Starting services`, info, "h_s_array_start"),
	r("emhttpd", `^Stopping services`, info, "h_s_array_stop"),
	r("emhttpd", `^unclean shutdown detected`, warn, "h_s_unclean"),
	r("kernel", `^mdcmd \(\d+\): check resume`, info, "h_s_parity_resume"),
	r("kernel", `^mdcmd \(\d+\): check`, info, "h_s_parity_start"),
	r("kernel", `^md: sync done`, info, "h_s_parity_done"),
	r("kernel", `^md: recovery thread: .*(incorrect|corrected)`, warn, "h_s_parity_err"),
	r(anyProg, `^mover: started`, info, "h_s_mover_start"),
	r(anyProg, `^mover: finished`, info, "h_s_mover_done"),
	// rc.docker has spelled the word with a single s.
	r(`rc\.docker`, `^(.+): started succes+fully`, info, "h_s_container_up", "name"),
	r(`rc\.docker`, `(?i)^start(ing|ed) docker`, info, "h_s_docker_up"),
	r(`rc\.docker`, `(?i)^stop(ping|ped) docker`, info, "h_s_docker_down"),
	r("kernel", iface+`: (?:\S+: \S+ )?(?:NIC )?Link is Up (?:- )?([\d.]+ ?[MG]bps)`, info, "h_s_link_up", "port", "speed"),
	r("kernel", iface+`: (?:NIC )?Link is Down`, warn, "h_s_link_down", "port"),
	r("kernel", `\(slave `+iface+`\): link status definitely down`, warn, "h_s_link_down", "port"),
	r("webgui", `^Successful login user (\S+) from (\S+)`, info, "h_s_web_ok", "user", "addr"),
	r("webgui", `^Unsuccessful login user (\S+) from (\S+)`, warn, "h_s_web_fail", "user", "addr"),
	r(sshd, `^Accepted \S+ for (\S+) from (\S+) port`, info, "h_s_ssh_ok", "user", "addr"),
	r(sshd, `^Failed \S+ for (?:invalid user )?(\S+) from (\S+) port`, warn, "h_s_ssh_fail", "user", "addr"),
	r("kernel", `^Memory cgroup out of memory: Killed process \d+ \(([^)]+)\)`, warn, "h_s_oom_cg", "proc"),
	r("kernel", `^Out of memory: Killed process \d+ \(([^)]+)\)`, fault, "h_s_oom", "proc"),
	r("kernel", `^XFS \((\w+)\): .*Shutting down filesystem`, fault, "h_s_fs_off", "disk"),
	r("kernel", `^XFS \((\w+)\): .*(?i:corruption)`, fault, "h_s_fs", "disk"),
	r("kernel", `^BTRFS (?:error|critical) \(device (\w+)\)`, fault, "h_s_fs", "disk"),
	r("kernel", `^EXT4-fs error \(device (\w+)\)`, fault, "h_s_fs", "disk"),
	quiet(r("emhttpd", `^spinning down /dev/(\w+)`, info, "h_s_spun_down", "dev")),

	noise("kernel", `^(docker\d+|br[-\w]*|virbr\d+|veth\w+|vnet\d+|bond\d+): port \d+\(.+\) entered \w+ state`),
	noise("kernel", `^(veth|vnet|eth|tap|virbr)\w*( \(unregistering\))?: (renamed from|entered|left) `),
	noise("kernel", `^IPv6: ADDRCONF\(NETDEV_\w+\): (veth|vnet|docker|br|virbr)`),
	noise("kernel", `^elogind`),
	noise("kernel", `^(\[|\s|\w+\+0x|(Call Trace|Mem-Info|Tasks state|Hardware name|CPU: |RIP: |Code: |R[A-Z0-9]{2}: |</?TASK>|Node \d|lowmem_reserve|workingset_|active_|inactive_|unevictable|isolated_|nr_|\d+ pages|Free swap|Total swap|Memory cgroup stats|memory: usage|swap: usage|oom-kill:|\w+ invoked oom-killer))`),
	noise("kernel", `^BTRFS info `),
	noise(`elogind(-daemon)?`, ``),
	noise(`avahi-daemon`, ``),
	noise(`wsdd2?`, ``),
	noise(`nmbd`, ``),
	noise(`smbd`, `(create_canon_ace_lists|posix_acls\.c|durable_reconnect|^\[\d{4}/)`),
	noise(sshd, `^(Accepted key|Postponed|Connection (from|closed)|Disconnected from|Received disconnect|User child|pam_unix\(sshd:session\)|Starting session|Invalid user|Close session|Server listening)`),
	noise("emhttpd", `^(read SMART|shcmd|cmd: |req \(\d+\))`),
	noise("move", ``),
	noise("crond", ``),
	noise("monitor_nchan", ``),
	noise("rsyslogd", ``),
	noise("ntpd", ``),
	noise("dhcpcd", `(rebinding|renewing|leased .* for|adding route|probing address)`),
	noise(`php-fpm`, `child \d+ (exited|started)`),
	noise("acpid", ``),
}

var (
	worse = regexp.MustCompile(`(?i)\b(panic|bug:|call trace|fatal exception)`)
	bad   = regexp.MustCompile(`(?i)\b(error|errors|fail|failed|failure|fatal|critical|timed out|timeout|denied|refused)\b`)
)

func classify(l line) class {
	for _, ru := range rules {
		if !ru.prog.MatchString(l.Prog) {
			continue
		}
		m := ru.msg.FindStringSubmatch(l.Msg)
		if m == nil {
			continue
		}
		c := class{Level: ru.level, Quiet: ru.quiet, Key: ru.key}
		for i, name := range ru.names {
			if c.Args == nil {
				c.Args = map[string]string{}
			}
			v := m[i+1]
			if name == "disk" {
				v = diskLabel(v)
			}
			c.Args[name] = v
		}
		return c
	}
	return class{Level: levelOf(l.Msg)}
}

var mdDev = regexp.MustCompile(`^md(\d+)(p\d+)?$`)

// diskLabel names a disk the way Unraid's Main page does: disk1 is Disk 1,
// parity2 is Parity 2, a pool keeps its name and md4p1, the array's device
// for disk 4, is Disk 4. A plain device such as sdb1 stays as it is.
func diskLabel(name string) string {
	if m := mdDev.FindStringSubmatch(name); m != nil {
		return "Disk " + m[1]
	}
	if strings.HasPrefix(name, "sd") || strings.HasPrefix(name, "nvme") || strings.HasPrefix(name, "loop") {
		return name
	}
	cut := strings.TrimRightFunc(name, unicode.IsDigit)
	if cut == "" {
		return name
	}
	label := strings.ToUpper(cut[:1]) + cut[1:]
	if n := name[len(cut):]; n != "" {
		label += " " + n
	}
	return label
}
