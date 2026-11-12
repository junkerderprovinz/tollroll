// Package board turns the reports and logs of the members of a fortress into
// what the tile shows: one row per app, however many devices it runs on, and
// one log with the lines of every app in time order.
//
// Apps are read only while someone looks: a view is built on request and
// reused for a few seconds. Each app's log is read on from where the last
// read stopped, separately for every language asked for.
package board

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/junkerderprovinz/parleyport/kit/status"
)

const (
	ttl       = 10 * time.Second
	perSource = 100
	maxLines  = 300
	maxLangs  = 4
	// firstPages bounds the paging through an app's whole log on the first
	// read; later reads pick up from the cursor and need far fewer.
	firstPages = 10
	// budget bounds the whole read of one app, report and log pages
	// together, so a slow app cannot keep the tile waiting.
	budget = 8 * time.Second
	// maxName and maxText cut what an app sends to what a tile row and a log
	// line can show.
	maxName = 60
	maxText = 300
)

// Unreachable is the state of a row whose app does not answer. It ranks
// between warning and overdue.
const Unreachable = "unreachable"

// Source is one member the tile reads. Name is what it calls its app, shown
// until it answers.
type Source struct {
	ID   string
	Name string
}

// Reader reads one member.
type Reader interface {
	Report(ctx context.Context, src Source, lang string) (status.Report, error)
	Log(ctx context.Context, src Source, after, lang string, limit int) (status.LogPage, error)
}

// View is everything the tile draws.
type View struct {
	Apps      []App     `json:"apps"`
	Log       []Line    `json:"log"`
	SampledAt time.Time `json:"sampledAt"`
}

// App is one row. Key is the app's ID from its report, or "source:" and the
// member ID while the app has never answered. Color is the app's own #rrggbb
// and Icon the ID of its logo in Icons, both empty when the app names none.
// Need and Work are what the row shows, Needs, Tasks and Next what the status
// window lists for every instance that answers. No address of the app is
// ever on the board.
type App struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	State     string `json:"state"`
	Instances int    `json:"instances"`
	Need      *Need  `json:"need,omitempty"`
	Work      *Work  `json:"work,omitempty"`
	Needs     []Need `json:"needs,omitempty"`
	Tasks     []Work `json:"tasks,omitempty"`
	Next      []Next `json:"next,omitempty"`
	Color     string `json:"color,omitempty"`
	Icon      string `json:"icon,omitempty"`
}

// Need is the most urgent thing an app asks the user to look at.
type Need struct {
	Severity string `json:"severity"`
	Text     string `json:"text"`
}

// Work is a task an app is running.
type Work struct {
	Title    string   `json:"title"`
	Progress *float64 `json:"progress,omitempty"`
}

// Next is something an app has planned.
type Next struct {
	Title string    `json:"title"`
	At    time.Time `json:"at"`
}

// listed caps each list of the status window.
const listed = 5

// Line is one line of the shared log. App is the key of its row.
type Line struct {
	ID    string    `json:"id"`
	At    time.Time `json:"at"`
	App   string    `json:"app"`
	Level string    `json:"level"`
	Text  string    `json:"text"`
}

// Board builds views. It is safe for concurrent use.
type Board struct {
	list   func() []Source
	reader Reader
	now    func() time.Time
	budget time.Duration

	mu     sync.Mutex
	state  map[string]*source
	cached map[string]cached
}

type cached struct {
	view View
	srcs []Source
}

type source struct {
	down    bool
	reports map[string]status.Report
	logs    map[string]*logBuf
}

type logBuf struct {
	cursor  string
	entries []status.LogEntry
}

// New returns a board over the sources list returns, read through r.
func New(list func() []Source, r Reader) *Board {
	return &Board{list: list, reader: r, now: time.Now, budget: budget, state: map[string]*source{}, cached: map[string]cached{}}
}

// View returns the view in lang, read fresh unless one younger than a few
// seconds exists for the same list of sources.
func (b *Board) View(ctx context.Context, lang string) View {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	srcs := b.list()
	if c, ok := b.cached[lang]; ok && now.Sub(c.view.SampledAt) < ttl && slices.Equal(c.srcs, srcs) {
		return c.view
	}
	b.read(ctx, srcs, lang)
	v := b.build(srcs, lang, now)
	evict(b.cached, lang)
	b.cached[lang] = cached{view: v, srcs: srcs}
	return v
}

func key(src Source) string { return src.ID }

func (b *Board) read(ctx context.Context, srcs []Source, lang string) {
	keep := map[string]bool{}
	var wg sync.WaitGroup
	for _, src := range srcs {
		k := key(src)
		keep[k] = true
		s := b.state[k]
		if s == nil {
			s = &source{reports: map[string]status.Report{}, logs: map[string]*logBuf{}}
			b.state[k] = s
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = b.readOne(ctx, src, s, lang)
		}()
	}
	wg.Wait()
	for k := range b.state {
		if !keep[k] {
			delete(b.state, k)
		}
	}
}

// readOne touches only s, so the sources can be read side by side. The error
// is the report's: a log that stops answering keeps what was read.
func (b *Board) readOne(ctx context.Context, src Source, s *source, lang string) error {
	ctx, cancel := context.WithTimeout(ctx, b.budget)
	defer cancel()
	rep, err := b.reader.Report(ctx, src, lang)
	if err != nil {
		s.down = true
		return err
	}
	s.down = false
	rep.App.ID = clip(rep.App.ID)
	rep.App.Name, rep.App.Version = cut(rep.App.Name, maxName), cut(rep.App.Version, maxName)
	if status.CheckColor(rep.App.Color) != nil {
		rep.App.Color = ""
	}
	if status.CheckIcon(rep.App.Icon) != nil {
		rep.App.Icon = ""
	}
	if _, ok := rank[string(rep.State)]; !ok {
		rep.State = Unreachable
	}
	evict(s.reports, lang)
	s.reports[lang] = rep

	buf := s.logs[lang]
	pages := 3
	if buf == nil {
		evict(s.logs, lang)
		buf = &logBuf{}
		s.logs[lang] = buf
		pages = firstPages
	}
	for range pages {
		page, err := b.reader.Log(ctx, src, buf.cursor, lang, status.MaxLogLimit)
		if err != nil {
			return nil
		}
		for _, e := range page.Entries {
			e.ID, e.Text = clip(e.ID), cut(e.Text, maxText)
			if _, ok := severityRank[e.Level]; !ok {
				e.Level = status.SeverityInfo
			}
			buf.entries = append(buf.entries, e)
		}
		if len(buf.entries) > perSource {
			buf.entries = slices.Clone(buf.entries[len(buf.entries)-perSource:])
		}
		if page.Next != "" {
			buf.cursor = page.Next
		}
		if len(page.Entries) < status.MaxLogLimit {
			return nil
		}
	}
	return nil
}

var rank = map[string]int{
	string(status.StateOff):      0,
	string(status.StateOK):       1,
	string(status.StateStarting): 2,
	string(status.StateWarning):  3,
	Unreachable:                  4,
	string(status.StateOverdue):  5,
	string(status.StateFailed):   6,
}

var severityRank = map[status.Severity]int{status.SeverityInfo: 0, status.SeverityWarn: 1, status.SeverityError: 2}

type instance struct {
	src   Source
	rep   status.Report
	known bool
	state string
}

func (b *Board) build(srcs []Source, lang string, now time.Time) View {
	rows := map[string][]instance{}
	var order []string
	keys := map[string]string{}
	for _, src := range srcs {
		s := b.state[key(src)]
		in := instance{src: src, state: Unreachable}
		if rep, ok := s.reports[lang]; ok && !s.down {
			in.rep, in.known, in.state = rep, true, string(rep.State)
		} else if rep, ok := s.reports[lang]; ok {
			in.rep, in.known = rep, true
		} else {
			for _, rep := range s.reports {
				in.rep, in.known = rep, true
				break
			}
		}
		k := "source:" + src.ID
		if in.known {
			k = in.rep.App.ID
		}
		if _, ok := rows[k]; !ok {
			order = append(order, k)
		}
		rows[k] = append(rows[k], in)
		keys[src.ID] = k
	}

	v := View{Apps: []App{}, Log: []Line{}, SampledAt: now}
	for _, k := range order {
		v.Apps = append(v.Apps, row(k, rows[k]))
	}
	slices.SortStableFunc(v.Apps, func(a, b App) int {
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})

	for _, src := range srcs {
		buf := b.state[key(src)].logs[lang]
		if buf == nil {
			continue
		}
		for _, e := range buf.entries {
			v.Log = append(v.Log, Line{ID: src.ID + ":" + e.ID, At: e.At, App: keys[src.ID], Level: string(e.Level), Text: e.Text})
		}
	}
	slices.SortStableFunc(v.Log, func(a, b Line) int { return a.At.Compare(b.At) })
	if len(v.Log) > maxLines {
		v.Log = v.Log[len(v.Log)-maxLines:]
	}
	return v
}

func row(k string, ins []instance) App {
	worst := ins[0]
	for _, in := range ins[1:] {
		if rank[in.state] > rank[worst.state] {
			worst = in
		}
	}
	a := App{Key: k, State: worst.state, Instances: len(ins)}
	for _, in := range ins {
		if a.Name == "" && in.rep.App.Name != "" {
			a.Name, a.Version = in.rep.App.Name, in.rep.App.Version
		}
		if a.Color == "" {
			a.Color = in.rep.App.Color
		}
		if a.Icon == "" && in.rep.App.Icon != "" {
			a.Icon = iconID(in.rep.App.Icon)
		}
	}
	if a.Name == "" {
		a.Name = fallbackName(worst.src)
	}
	if worst.known && worst.state != Unreachable {
		needs := slices.Clone(worst.rep.NeedsYou)
		slices.SortStableFunc(needs, func(x, y status.Need) int { return severityRank[y.Severity] - severityRank[x.Severity] })
		if len(needs) > 0 {
			a.Need = &Need{Severity: string(needs[0].Severity), Text: cut(needs[0].Text, maxText)}
		} else if len(worst.rep.Work) > 0 {
			w := worst.rep.Work[0]
			a.Work = &Work{Title: cut(w.Title, maxText), Progress: w.Progress}
		}
	}
	details(&a, ins)
	return a
}

// details fills the lists of the status window from every instance that
// answers, the most urgent need and the soonest plan first.
func details(a *App, ins []instance) {
	var needs []status.Need
	for _, in := range ins {
		if !in.known || in.state == Unreachable {
			continue
		}
		needs = append(needs, in.rep.NeedsYou...)
		for _, w := range in.rep.Work {
			a.Tasks = append(a.Tasks, Work{Title: cut(w.Title, maxText), Progress: w.Progress})
		}
		for _, n := range in.rep.Next {
			a.Next = append(a.Next, Next{Title: cut(n.Title, maxText), At: n.At})
		}
	}
	slices.SortStableFunc(needs, func(x, y status.Need) int { return severityRank[y.Severity] - severityRank[x.Severity] })
	for _, n := range needs {
		a.Needs = append(a.Needs, Need{Severity: string(n.Severity), Text: cut(n.Text, maxText)})
	}
	slices.SortStableFunc(a.Next, func(x, y Next) int { return x.At.Compare(y.At) })
	a.Needs, a.Tasks, a.Next = first(a.Needs), first(a.Tasks), first(a.Next)
}

func first[T any](l []T) []T {
	if len(l) > listed {
		return l[:listed]
	}
	return l
}

// Icons returns the logos of the apps on the board by the ID their rows carry,
// as data URIs that passed status.CheckIcon. The tile asks for them only when
// a row names one it has not seen, so the board stays small on every poll.
func (b *Board) Icons() map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]string{}
	for _, s := range b.state {
		for _, rep := range s.reports {
			if rep.App.Icon != "" {
				out[iconID(rep.App.Icon)] = rep.App.Icon
			}
		}
	}
	return out
}

func iconID(icon string) string {
	sum := sha256.Sum256([]byte(icon))
	return hex.EncodeToString(sum[:8])
}

// Seen is how one source looked at its last read. Needs are those of its last
// answer and stay while the app is down, so a reader can tell a returning need
// from a new one.
type Seen struct {
	Source string
	Name   string
	Down   bool
	Needs  []status.Need
}

// Check reads the sources as View does and returns each one in list order.
func (b *Board) Check(ctx context.Context, lang string) []Seen {
	b.View(ctx, lang)
	b.mu.Lock()
	defer b.mu.Unlock()
	srcs := b.list()
	out := make([]Seen, 0, len(srcs))
	for _, src := range srcs {
		s, ok := b.state[key(src)]
		if !ok {
			continue
		}
		in := instance{src: src}
		if rep, ok := s.reports[lang]; ok {
			in.rep, in.known = rep, true
		}
		seen := Seen{Source: src.ID, Name: in.rep.App.Name, Down: s.down || !in.known, Needs: unlinked(in.rep.NeedsYou)}
		if seen.Name == "" {
			seen.Name = fallbackName(src)
		}
		out = append(out, seen)
	}
	return out
}

// unlinked copies needs without the links an app may give them, which lead
// to its own address.
func unlinked(needs []status.Need) []status.Need {
	out := slices.Clone(needs)
	for i := range out {
		out[i].Link = ""
	}
	return out
}

// fallbackName names an app that never answered by the app its member said
// it runs.
func fallbackName(src Source) string {
	if src.Name != "" {
		return src.Name
	}
	return src.ID
}

func cut(s string, runes int) string {
	if utf8.RuneCountInString(s) <= runes {
		return s
	}
	return string([]rune(s)[:runes-1]) + "…"
}

// clip shortens an ID an app chose, which ends up in every log line.
func clip(s string) string {
	if len(s) <= maxName {
		return s
	}
	return strings.ToValidUTF8(s[:maxName], "")
}

// evict makes room for lang in a map kept per language, so a stream of
// language tags cannot grow it without end.
func evict[V any](m map[string]V, lang string) {
	if _, ok := m[lang]; ok || len(m) < maxLangs {
		return
	}
	for l := range m {
		delete(m, l)
		return
	}
}
