package fortress

import (
	"errors"
	"path/filepath"
	"testing"

	kit "github.com/junkerderprovinz/parleyport/kit/fortress"
	"github.com/junkerderprovinz/parleyport/kit/group"
	"github.com/junkerderprovinz/parleyport/kit/member"
)

func TestTheMapShowsEachMembersLogoAndWhetherOnlyTheRelayReachesIt(t *testing.T) {
	m := kit.Member{ID: "m", Kind: member.KindInstance, App: kit.App{ID: "shiplog", Name: "ShipLog", Icon: Icon}, State: group.StateApproved}
	for _, c := range []struct {
		direct, relay bool
		want          bool
	}{{false, true, true}, {true, true, false}, {true, false, false}, {false, false, false}} {
		m.Direct, m.Relay = c.direct, c.relay
		got, _ := entry(m)
		if got.Relay != c.want {
			t.Errorf("direct %v, relay %v: via relay %v, want %v", c.direct, c.relay, got.Relay, c.want)
		}
		if got.Icon != Icon {
			t.Errorf("the logo is %q", got.Icon)
		}
	}
	m.App.Icon = "javascript:alert(1)"
	if got, _ := entry(m); got.Icon != "" {
		t.Errorf("a logo that is no SVG data URI is on the map: %q", got.Icon)
	}
}

func TestAMembersStatusIsReadByItsID(t *testing.T) {
	url := relayServer(t)
	secret, words := newPhrase(t)
	openApp(t, url, secret, "alpha", "Alpha", "cellar", app{id: "alpha", name: "Alpha"})
	s := openService(t, filepath.Join(t.TempDir(), "fortress"), url)
	if _, err := s.Source("x"); !errors.Is(err, ErrNotJoined) {
		t.Errorf("outside a fortress: %v", err)
	}
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	id := memberID(t, s, "alpha")
	src, err := s.Source(id)
	if err != nil || src.ID != id || src.Name != "Alpha" {
		t.Errorf("alpha is %+v, %v", src, err)
	}
	if _, err := s.Source("stranger"); !errors.Is(err, ErrNoMember) {
		t.Errorf("a stranger: %v", err)
	}
}
