package host

import (
	"strconv"
	"strings"
	"time"
)

// uncleanWithin is how soon after the array starts Unraid begins the parity
// check an unclean shutdown calls for.
const uncleanWithin = 10 * time.Minute

// parityCause words a parity check that started at at by who started it: the
// one Unraid begins as the array starts after an unclean shutdown, one at a
// time the parity schedule names, or else one started by hand or a script.
func (h *Host) parityCause(at time.Time) string {
	unclean := h.unclean && at.Sub(h.arrayAt) < uncleanWithin
	h.unclean = false
	if unclean {
		return "h_s_parity_unclean"
	}
	for _, l := range readCron(h.o.Root) {
		f := strings.Fields(l)
		if len(f) > 5 && strings.Contains(l, "mdcmd check") && (cronAt(f[:5], at) || cronAt(f[:5], at.Add(-time.Minute))) {
			return "h_s_parity_schedule"
		}
	}
	return "h_s_parity_hand"
}

// cronAt reports whether the five time fields of a crontab line name the
// minute of t. As in cron, a day of the month and a day of the week that are
// both set match on either.
func cronAt(f []string, t time.Time) bool {
	if !cronField(f[0], t.Minute(), 0) || !cronField(f[1], t.Hour(), 0) || !cronField(f[3], int(t.Month()), 1) {
		return false
	}
	dow := int(t.Weekday())
	dom, wd := cronField(f[2], t.Day(), 1), cronField(f[4], dow, 0) || (dow == 0 && cronField(f[4], 7, 0))
	if f[2] != "*" && f[4] != "*" {
		return dom || wd
	}
	return dom && wd
}

// cronField reports whether v is in a field of lists, ranges and steps.
// first is where * starts.
func cronField(field string, v, first int) bool {
	for _, part := range strings.Split(field, ",") {
		span, step, stepped := strings.Cut(part, "/")
		every := 1
		if stepped {
			n, err := strconv.Atoi(step)
			if err != nil || n < 1 {
				return false
			}
			every = n
		}
		lo, hi := first, 1<<30
		if span != "*" {
			a, b, isRange := strings.Cut(span, "-")
			var err error
			if lo, err = strconv.Atoi(a); err != nil {
				return false
			}
			hi = lo
			if isRange {
				if hi, err = strconv.Atoi(b); err != nil {
					return false
				}
			} else if stepped {
				hi = 1 << 30
			}
		}
		if v >= lo && v <= hi && (v-lo)%every == 0 {
			return true
		}
	}
	return false
}
