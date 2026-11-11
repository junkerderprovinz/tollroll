package prefs

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestTheTileStartsWithEverything(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "tile.json"))
	if got := s.Get(); got.View != ViewAll {
		t.Errorf("view %q, want %q", got.View, ViewAll)
	}
}

func TestTheChosenViewOutlivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins", "tollroll", "tile.json")
	if err := Open(path).Set(Prefs{View: ViewLog}); err != nil {
		t.Fatal(err)
	}
	if got := Open(path).Get(); got.View != ViewLog {
		t.Errorf("after a restart the view is %q", got.View)
	}
}

func TestEveryViewOfTheTileIsKept(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "tile.json"))
	for _, v := range []string{ViewAll, ViewApps, ViewLog, ViewMap} {
		if err := s.Set(Prefs{View: v}); err != nil || s.Get().View != v {
			t.Errorf("Set(%q) = %v, view %q", v, err, s.Get().View)
		}
	}
}

func TestAnUnknownViewIsRefusedAndNothingChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tile.json")
	s := Open(path)
	if err := s.Set(Prefs{View: ViewApps}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"", "MAP", "LOG", "../x"} {
		if err := s.Set(Prefs{View: v}); !errors.Is(err, ErrView) {
			t.Errorf("Set(%q) = %v, want ErrView", v, err)
		}
	}
	if got := Open(path).Get(); got.View != ViewApps {
		t.Errorf("view %q after refused changes", got.View)
	}
}

func TestADamagedFileFallsBackToEverything(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tile.json")
	for _, raw := range []string{"{", `{"view":"sideways"}`} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := Open(path).Get(); got.View != ViewAll {
			t.Errorf("%s gives view %q", raw, got.View)
		}
	}
}

func TestTheAppsLeftOutOutliveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tile.json")
	if err := Open(path).Set(Prefs{View: ViewLog, Hidden: []string{"bravo", "alpha", "bravo"}}); err != nil {
		t.Fatal(err)
	}
	got := Open(path).Get()
	if strings.Join(got.Hidden, ",") != "alpha,bravo" {
		t.Errorf("hidden %v, want alpha,bravo once each", got.Hidden)
	}
	if got := Open(filepath.Join(t.TempDir(), "new.json")).Get(); got.Hidden == nil || len(got.Hidden) != 0 {
		t.Errorf("a new tile hides %#v, want an empty list", got.Hidden)
	}
}

func TestAnOverlongListOfAppsIsRefused(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "tile.json"))
	many := make([]string, maxHidden+1)
	for i := range many {
		many[i] = strconv.Itoa(i)
	}
	for _, hidden := range [][]string{many, {strings.Repeat("a", maxID+1)}, {""}} {
		if err := s.Set(Prefs{View: ViewAll, Hidden: hidden}); !errors.Is(err, ErrHidden) {
			t.Errorf("Set(%d apps) = %v, want ErrHidden", len(hidden), err)
		}
	}
}
