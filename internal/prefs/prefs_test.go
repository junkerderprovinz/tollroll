package prefs

import (
	"errors"
	"os"
	"path/filepath"
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
