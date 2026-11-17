package advisor

import (
	"path/filepath"
	"testing"
)

func ids(s []Suggestion) []string {
	out := []string{}
	for _, x := range s {
		out = append(out, x.ID)
	}
	return out
}

func TestTheAdvisorSuggestsWhatNothingTakesCareOf(t *testing.T) {
	for _, c := range []struct {
		name string
		f    Facts
		want string
	}{
		{"a bare server", Facts{Unraid: true}, "backup,health"},
		{"with containers", Facts{Unraid: true, Containers: []string{"jellyfin"}}, "backup,updates,health"},
		{"with the apps in the fortress", Facts{Unraid: true, Containers: []string{"x"}, Apps: []string{"bombvault", "shiplog"}, Plugins: []string{"firesquire"}}, ""},
		{"with a backup container", Facts{Unraid: true, Containers: []string{"BombVault"}}, "updates,health"},
		{"while another app backs up", Facts{Unraid: true, Backs: true}, "health"},
		{"off Unraid", Facts{}, ""},
	} {
		got := ""
		for i, id := range ids(Suggest(c.f)) {
			if i > 0 {
				got += ","
			}
			got += id
		}
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if s := Suggest(Facts{Unraid: true}); s[0].Link != "https://github.com/junkerderprovinz/bombvault" || s[0].Text != "a_backup" {
		t.Errorf("%+v", s[0])
	}
}

func TestADismissedSuggestionStaysAwayAndTheFirstLookComesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "advice.json")
	st := Open(path)
	f := Facts{Unraid: true}
	if got := ids(st.First(f)); len(got) != 2 {
		t.Fatalf("first look %q", got)
	}
	if got := st.First(f); len(got) != 0 {
		t.Errorf("a second first look %+v", got)
	}
	if err := st.Dismiss("backup"); err != nil {
		t.Fatal(err)
	}
	if err := st.Dismiss("nothing"); err != ErrNoSuggestion {
		t.Errorf("%v", err)
	}
	again := Open(path)
	if got := ids(again.List(f)); len(got) != 1 || got[0] != "health" {
		t.Errorf("%q", got)
	}
	if got := again.First(f); len(got) != 0 {
		t.Errorf("first look after a restart %+v", got)
	}
}
