package fortress

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	kit "github.com/junkerderprovinz/parleyport/kit/fortress"
	"github.com/junkerderprovinz/parleyport/kit/group"
	"github.com/junkerderprovinz/parleyport/kit/lan"
	"github.com/junkerderprovinz/parleyport/kit/member"
	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/journal"
)

// testNetwork points the fortress's announces at a group of the test's own,
// or skips the test on a host that cannot join one.
func testNetwork(t *testing.T) {
	t.Helper()
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).AddrPort().Port()
	_ = c.Close()
	addr := netip.AddrPortFrom(netip.MustParseAddr("239.255.77.251"), port)
	m, err := net.ListenMulticastUDP("udp4", nil, net.UDPAddrFromAddrPort(addr))
	if err != nil {
		t.Skipf("this host cannot join a multicast group: %v", err)
	}
	_ = m.Close()
	was := kit.Multicast
	kit.Multicast = addr
	t.Cleanup(func() { kit.Multicast = was })
}

// homeVault opens a backup app that takes no relay and answers direct calls
// on this network.
func homeVault(t *testing.T, secret []byte) *kit.Fortress {
	t.Helper()
	ident, err := member.NewIdentity(member.KindInstance)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	l := lan.New(lan.Config{Group: kit.Multicast})
	l.Start()
	t.Cleanup(func() { _ = l.Close() })
	f, err := kit.Open(kit.Config{
		App: kit.App{ID: "bombvault", Name: "BombVault", Version: "1.0.0"}, Secret: secret, Identity: ident,
		Self:  group.Self{Name: "tower", Deployment: "container", DirectURL: srv.URL},
		Relay: group.RelayOff, LAN: l, Store: &memStore{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	mux.Handle(group.DirectPath, f.DirectHandler())
	return f
}

func TestWithoutARelayBombVaultReadsTheTilesStatusOnThisNetwork(t *testing.T) {
	testNetwork(t)
	secret, words := newPhrase(t)
	bv := homeVault(t, secret)
	j := &journal.Journal{App: status.App{ID: AppID, Name: "TollRoll", Version: "0.2.0"}, Every: time.Minute,
		Texts: func(string) map[string]string { return map[string]string{} }}
	s, err := Open(Options{Dir: filepath.Join(t.TempDir(), "fortress"), Device: "tower", Version: "0.2.0", Relay: "off", LAN: true, Journal: j})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Join(words); err != nil {
		t.Fatal(err)
	}
	var tile string
	waitFor(t, "BombVault to reach the tile on the network", func() bool {
		for _, m := range bv.Members() {
			if m.App.ID == AppID && m.Direct {
				tile = m.ID
				return true
			}
		}
		return false
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rep, err := bv.Status(ctx, tile, "en")
	if err != nil || rep.App.ID != AppID {
		t.Fatalf("the tile's status = %+v, %v", rep, err)
	}
}
