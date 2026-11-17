package plugins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/board"
)

const report = `{"contract":1,"app":{"id":"firesquire","name":"FireSquire","version":"1.5.0"},"state":"warning",
"needsYou":[{"id":"smart-sdb","severity":"warn","code":"smart","text":"Disk 2 has more pending sectors"}]}`

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAPluginsReportFileIsASourceOfTheBoard(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "firesquire.json", report)
	write(t, dir, "firesquire.de.json", `{"contract":1,"app":{"id":"firesquire","name":"FireSquire"},"state":"ok"}`)
	write(t, dir, "notes.txt", "x")
	f := Files{Dir: dir}
	srcs := f.Sources()
	if len(srcs) != 1 || srcs[0] != (board.Source{ID: "file:firesquire", Name: "firesquire"}) {
		t.Fatalf("%+v", srcs)
	}
	rep, err := f.Report(context.Background(), srcs[0], "en")
	if err != nil || rep.State != status.StateWarning || len(rep.NeedsYou) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if rep, _ := f.Report(context.Background(), srcs[0], "de-AT"); rep.State != status.StateOK {
		t.Errorf("the German report: %+v", rep)
	}

	list, r := Join(func() []board.Source { return nil }, nil, f)
	b := board.New(list, r)
	v := b.View(context.Background(), "en")
	if len(v.Apps) != 1 || v.Apps[0].Key != "firesquire" || v.Apps[0].Need == nil {
		t.Errorf("%+v", v.Apps)
	}
}

func TestAnOldReportReadsAsAPluginThatDoesNotAnswer(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "firesquire.json", report)
	f := Files{Dir: dir, Now: func() time.Time { return time.Now().Add(2 * time.Hour) }}
	if _, err := f.Report(context.Background(), board.Source{ID: "file:firesquire"}, "en"); !errors.Is(err, ErrStale) {
		t.Errorf("%v", err)
	}
}

func TestAReportForAnotherAppOrBehindALinkIsRefused(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "shiplog.json", report)
	f := Files{Dir: dir}
	if _, err := f.Report(context.Background(), board.Source{ID: "file:shiplog"}, "en"); err == nil {
		t.Error("a report that names another app")
	}
	other := filepath.Join(t.TempDir(), "secret.json")
	write(t, filepath.Dir(other), "secret.json", report)
	if err := os.Symlink(other, filepath.Join(dir, "firesquire.json")); err != nil {
		t.Skip("no symlinks here")
	}
	if _, err := f.Report(context.Background(), board.Source{ID: "file:firesquire"}, "en"); err == nil {
		t.Error("read through a link")
	}
}
