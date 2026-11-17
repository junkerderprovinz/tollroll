// Package plugins reads the status of the plugins on this server that report
// through a file instead of a fortress member of their own, such as a plugin
// whose member joins only while one of its checks runs.
//
// A plugin writes its report, in the format of kit/status, to Dir/<app>.json,
// where <app> is the report's app ID, and may write one per language as
// Dir/<app>.<lang>.json. It writes the file again at least once an hour,
// replacing it in one rename; a report older than that reads as an app that
// does not answer. Dir lives in memory, so reading it wakes no disk.
package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/board"
)

// Dir is where the plugins of an Unraid server write their reports.
const Dir = "/var/local/fortress"

const (
	// prefix marks the board's sources that are files.
	prefix = "file:"
	// staleAfter is how old a report may be before the plugin counts as
	// not answering.
	staleAfter = time.Hour
	maxReport  = 256 << 10
)

// ErrStale is a report the plugin has not written for too long.
var ErrStale = errors.New("plugins: the report is out of date")

var (
	appFile  = regexp.MustCompile(`^([a-z][a-z0-9-]{0,59})\.json$`)
	langCode = regexp.MustCompile(`^[a-z]{2,3}$`)
)

// Files reads the reports in Dir.
type Files struct {
	Dir string
	Now func() time.Time
}

func (f Files) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// Sources lists one source per plugin that writes a report.
func (f Files) Sources() []board.Source {
	entries, _ := os.ReadDir(f.Dir)
	var out []board.Source
	for _, e := range entries {
		if m := appFile.FindStringSubmatch(e.Name()); m != nil && e.Type().IsRegular() {
			out = append(out, board.Source{ID: prefix + m[1], Name: m[1]})
		}
	}
	return out
}

// Report reads the report of src, in lang where the plugin wrote one.
func (f Files) Report(_ context.Context, src board.Source, lang string) (status.Report, error) {
	app := strings.TrimPrefix(src.ID, prefix)
	var rep status.Report
	b, fi, err := f.read(app, lang)
	if err != nil {
		return rep, err
	}
	if f.now().Sub(fi.ModTime()) > staleAfter {
		return rep, ErrStale
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		return rep, err
	}
	if rep.App.ID != app {
		return status.Report{}, errors.New("plugins: the report names another app")
	}
	return rep, nil
}

// read takes the report in lang, else the plain one. Only regular files
// count, so a link cannot point the service at another file.
func (f Files) read(app, lang string) ([]byte, os.FileInfo, error) {
	primary, _, _ := strings.Cut(strings.ToLower(lang), "-")
	names := []string{app + ".json"}
	if langCode.MatchString(primary) {
		names = slices.Insert(names, 0, app+"."+primary+".json")
	}
	var err error
	for _, n := range names {
		var fi os.FileInfo
		p := filepath.Join(f.Dir, n)
		if fi, err = os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		var file *os.File
		if file, err = os.Open(p); err != nil {
			continue
		}
		b, rerr := io.ReadAll(io.LimitReader(file, maxReport+1))
		_ = file.Close()
		if rerr != nil || len(b) > maxReport {
			return nil, nil, errors.New("plugins: the report is too large")
		}
		return b, fi, nil
	}
	if err == nil {
		err = os.ErrNotExist
	}
	return nil, nil, err
}

// Log answers that a plugin's file has no log.
func (Files) Log(_ context.Context, _ board.Source, after, _ string, _ int) (status.LogPage, error) {
	return status.LogPage{Entries: []status.LogEntry{}, Next: after}, nil
}

// Join reads the members of the fortress through r and the plugins' files
// through f, as one list for the board.
func Join(members func() []board.Source, r board.Reader, f Files) (func() []board.Source, board.Reader) {
	list := func() []board.Source { return append(members(), f.Sources()...) }
	return list, joined{r: r, f: f}
}

type joined struct {
	r board.Reader
	f Files
}

func (j joined) Report(ctx context.Context, src board.Source, lang string) (status.Report, error) {
	if strings.HasPrefix(src.ID, prefix) {
		return j.f.Report(ctx, src, lang)
	}
	return j.r.Report(ctx, src, lang)
}

func (j joined) Log(ctx context.Context, src board.Source, after, lang string, limit int) (status.LogPage, error) {
	if strings.HasPrefix(src.ID, prefix) {
		return j.f.Log(ctx, src, after, lang, limit)
	}
	return j.r.Log(ctx, src, after, lang, limit)
}
