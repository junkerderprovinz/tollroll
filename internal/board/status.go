package board

import (
	"context"
	"slices"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/status"
)

// statusLines is how many log lines the status window of a member shows.
const statusLines = 10

// Status is one member as its status window on the settings page shows it:
// the state and lists of its report and its latest log lines, newest first.
// Like the board, it carries no address of the app.
type Status struct {
	Report Report  `json:"report"`
	Log    []Entry `json:"log"`
}

// Report is what the status window shows of a member's report.
type Report struct {
	State    string `json:"state"`
	NeedsYou []Need `json:"needsYou"`
	Work     []Work `json:"work"`
	Next     []Next `json:"next"`
}

// Entry is one line of a member's log.
type Entry struct {
	At    time.Time `json:"at"`
	Level string    `json:"level"`
	Text  string    `json:"text"`
}

// Status reads member src for its status window. A member on the board is
// read on from where the board left off, any other from the start of its log,
// and neither gets a row for it.
func (b *Board) Status(ctx context.Context, src Source, lang string) (Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.state[key(src)]
	if s == nil {
		s = &source{reports: map[string]status.Report{}, logs: map[string]*logBuf{}}
	}
	if err := b.readOne(ctx, src, s, lang); err != nil {
		return Status{}, err
	}
	rep := s.reports[lang]
	out := Status{Report: Report{State: string(rep.State), NeedsYou: []Need{}, Work: []Work{}, Next: []Next{}}, Log: []Entry{}}
	needs := slices.Clone(rep.NeedsYou)
	slices.SortStableFunc(needs, func(x, y status.Need) int { return severityRank[y.Severity] - severityRank[x.Severity] })
	for _, n := range first(needs) {
		out.Report.NeedsYou = append(out.Report.NeedsYou, Need{Severity: string(n.Severity), Text: cut(n.Text, maxText)})
	}
	for _, w := range first(rep.Work) {
		out.Report.Work = append(out.Report.Work, Work{Title: cut(w.Title, maxText), Progress: w.Progress})
	}
	next := slices.Clone(rep.Next)
	slices.SortStableFunc(next, func(x, y status.Scheduled) int { return x.At.Compare(y.At) })
	for _, n := range first(next) {
		out.Report.Next = append(out.Report.Next, Next{Title: cut(n.Title, maxText), At: n.At})
	}
	if buf := s.logs[lang]; buf != nil {
		for i := len(buf.entries) - 1; i >= 0 && len(out.Log) < statusLines; i-- {
			e := buf.entries[i]
			out.Log = append(out.Log, Entry{At: e.At, Level: string(e.Level), Text: e.Text})
		}
	}
	return out, nil
}
