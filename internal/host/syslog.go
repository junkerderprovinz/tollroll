package host

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// line is one syslog line split into its parts. Host is left out, it is
// always this server.
type line struct {
	At   time.Time
	Prog string
	Msg  string
}

// raw is the line without its time and host, as the log shows it on hover.
func (l line) raw() string { return l.Prog + ": " + l.Msg }

var (
	classic = regexp.MustCompile(`^([A-Z][a-z]{2} [ \d]\d \d\d:\d\d:\d\d) \S+ ([^:\[\s]+)(?:\[\d+\])?: ?(.*)$`)
	rfc3339 = regexp.MustCompile(`^(\d{4}-\d\d-\d\dT\S+) \S+ ([^:\[\s]+)(?:\[\d+\])?: ?(.*)$`)
)

// parse reads a line of rsyslog's classic format or of its RFC 3339 format.
func parse(s string, now time.Time) (line, bool) {
	m := classic.FindStringSubmatch(s)
	if m == nil {
		m = rfc3339.FindStringSubmatch(s)
	}
	if m == nil {
		return line{}, false
	}
	at, ok := stamp(m[1], now)
	return line{At: at, Prog: m[2], Msg: m[3]}, ok
}

// stamp reads the time of a line. The classic one has no year and is taken
// as the latest one not after now.
func stamp(s string, now time.Time) (time.Time, bool) {
	if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return at, true
	}
	at, err := time.ParseInLocation(time.Stamp, s, now.Location())
	if err != nil {
		return time.Time{}, false
	}
	at = at.AddDate(now.Year(), 0, 0)
	if at.After(now.Add(24 * time.Hour)) {
		at = at.AddDate(-1, 0, 0)
	}
	return at, true
}

// firstRead bounds how much of each file the first look reads. Unraid's
// syslog can grow by tens of megabytes a day, and the log keeps only its
// latest lines anyway.
const firstRead = 2 << 20

// tail reads a log file on from where it stopped, through rotation: when the
// file at path is a new one, the rest of the old one is read from path.1
// first.
type tail struct {
	path string
	fi   os.FileInfo
	off  int64
}

func (t *tail) read(each func(string)) {
	fi, err := os.Stat(t.path)
	if err != nil {
		return
	}
	if t.fi == nil {
		if old, err := os.Stat(t.path + ".1"); err == nil {
			readFrom(t.path+".1", max(0, old.Size()-firstRead), each)
		}
		t.fi, t.off = fi, readFrom(t.path, max(0, fi.Size()-firstRead), each)
		return
	}
	if !os.SameFile(t.fi, fi) {
		if old, err := os.Stat(t.path + ".1"); err == nil && os.SameFile(t.fi, old) {
			readFrom(t.path+".1", t.off, each)
		}
		t.off = 0
	} else if fi.Size() < t.off {
		t.off = 0
	}
	t.fi, t.off = fi, readFrom(t.path, t.off, each)
}

// readFrom hands each whole line from off on to each and returns where the
// last whole line ends. A start inside a line skips to the next one.
func readFrom(path string, off int64, each func(string)) int64 {
	f, err := os.Open(path)
	if err != nil {
		return off
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return off
	}
	r := bufio.NewReaderSize(f, 64<<10)
	if off > 0 {
		if !atLineStart(f, off) {
			skipped, err := r.ReadString('\n')
			if err != nil {
				return off
			}
			off += int64(len(skipped))
		}
	}
	for {
		s, err := r.ReadString('\n')
		if err != nil {
			return off
		}
		off += int64(len(s))
		each(strings.TrimRight(s, "\r\n"))
	}
}

func atLineStart(f *os.File, off int64) bool {
	b := make([]byte, 1)
	_, err := f.ReadAt(b, off-1)
	return err == nil && b[0] == '\n'
}
