package fortress

import (
	"testing"
)

func TestTheTileThatLeavesIsGoneFromTheOtherMembers(t *testing.T) {
	url := relayServer(t)
	secret, words := newPhrase(t)
	a := openApp(t, url, secret, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	s := openService(t, t.TempDir()+"/fortress", url)
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	listed := func() bool {
		for _, m := range a.Members() {
			if m.App.ID == AppID {
				return true
			}
		}
		return false
	}
	waitFor(t, "alpha to see the tile", listed)

	if err := s.Leave(); err != nil {
		t.Fatal(err)
	}
	if listed() {
		t.Error("alpha still lists the tile after it left")
	}
}
