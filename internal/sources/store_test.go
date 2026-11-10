package sources

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.json")
	s, err := Open(path, filepath.Join(dir, "missing.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestAStoreStartsEmpty(t *testing.T) {
	s, _ := open(t)
	if got := s.List(); len(got) != 0 {
		t.Fatalf("new store lists %d sources", len(got))
	}
}

func TestReplaceSavesAddressAndToken(t *testing.T) {
	s, path := open(t)
	pub, err := s.Replace([]Edit{{Address: "https://tower.lan:3443/", Token: "secret-token"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(pub) != 1 || pub[0].Address != "https://tower.lan:3443" || !pub[0].HasToken || pub[0].Kind != KindURL {
		t.Fatalf("public list %+v", pub)
	}

	again, err := Open(path, "")
	if err != nil {
		t.Fatal(err)
	}
	list := again.List()
	if len(list) != 1 || list[0].Token != "secret-token" || list[0].ID != pub[0].ID {
		t.Fatalf("reopened store lists %+v", list)
	}
}

func TestThePublicListCarriesNoToken(t *testing.T) {
	s, _ := open(t)
	pub, err := s.Replace([]Edit{{Address: "https://tower.lan:3443", Token: "secret-token"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(pub)
	if strings.Contains(string(b), "secret-token") || strings.Contains(strings.ToLower(string(b)), `"token"`) {
		t.Fatalf("public list leaks the token: %s", b)
	}
}

func TestAnEmptyTokenKeepsTheSavedOne(t *testing.T) {
	s, _ := open(t)
	pub, _ := s.Replace([]Edit{{Address: "https://tower.lan:3443", Token: "first"}})
	if _, err := s.Replace([]Edit{{ID: pub[0].ID, Address: "https://tower.lan:3443"}}); err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0].Token; got != "first" {
		t.Fatalf("token %q, want the saved one", got)
	}
}

func TestANewTokenReplacesTheSavedOne(t *testing.T) {
	s, _ := open(t)
	pub, _ := s.Replace([]Edit{{Address: "https://tower.lan:3443", Token: "first"}})
	if _, err := s.Replace([]Edit{{ID: pub[0].ID, Address: "https://tower.lan:3443", Token: "second"}}); err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0].Token; got != "second" {
		t.Fatalf("token %q, want second", got)
	}
}

func TestLeavingASourceOutRemovesIt(t *testing.T) {
	s, _ := open(t)
	pub, _ := s.Replace([]Edit{
		{Address: "https://a.lan:3443", Token: "a"},
		{Address: "https://b.lan:3443", Token: "b"},
	})
	if _, err := s.Replace([]Edit{{ID: pub[1].ID, Address: "https://b.lan:3443"}}); err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 1 || list[0].Token != "b" {
		t.Fatalf("list %+v", list)
	}
}

func TestASocketNeedsNoToken(t *testing.T) {
	s, _ := open(t)
	pub, err := s.Replace([]Edit{{Address: "/var/run/example.sock"}})
	if err != nil {
		t.Fatal(err)
	}
	if pub[0].Kind != KindSocket || pub[0].HasToken {
		t.Fatalf("socket source %+v", pub[0])
	}
}

func TestAWebAddressNeedsAToken(t *testing.T) {
	s, _ := open(t)
	_, err := s.Replace([]Edit{{Address: "https://tower.lan:3443"}})
	var ae *AddressError
	if !errors.As(err, &ae) || ae.Code != "token_missing" || ae.Index != 0 {
		t.Fatalf("err %v, want token_missing for row 0", err)
	}
}

func TestInvalidAddressesAreRefused(t *testing.T) {
	for _, addr := range []string{
		"",
		"tower.lan:3443",
		"ftp://tower.lan",
		"https://",
		"https://user:pw@tower.lan",
		"https://tower.lan/?token=x",
		"javascript:alert(1)",
		"/var/run/../etc/shadow",
		"/var/run/example",
		"relative.sock",
	} {
		s, _ := open(t)
		_, err := s.Replace([]Edit{{Address: addr, Token: "t"}})
		var ae *AddressError
		if !errors.As(err, &ae) || ae.Code != "invalid_address" {
			t.Errorf("%q: err %v, want invalid_address", addr, err)
		}
	}
}

func TestAWebAddressMayCarryAPathForAReverseProxy(t *testing.T) {
	s, _ := open(t)
	pub, err := s.Replace([]Edit{{Address: "https://apps.example.org/vault/", Token: "t"}})
	if err != nil {
		t.Fatal(err)
	}
	if pub[0].Address != "https://apps.example.org/vault" {
		t.Fatalf("address %q", pub[0].Address)
	}
}

func TestTheSameAddressTwiceIsRefused(t *testing.T) {
	s, _ := open(t)
	_, err := s.Replace([]Edit{
		{Address: "https://tower.lan:3443", Token: "a"},
		{Address: "https://tower.lan:3443/", Token: "b"},
	})
	var ae *AddressError
	if !errors.As(err, &ae) || ae.Code != "duplicate" || ae.Index != 1 {
		t.Fatalf("err %v, want duplicate for row 1", err)
	}
}

func TestAnUnknownIDIsRefused(t *testing.T) {
	s, _ := open(t)
	_, err := s.Replace([]Edit{{ID: "feedbeef", Address: "/var/run/x.sock"}})
	var ae *AddressError
	if !errors.As(err, &ae) || ae.Code != "unknown_source" {
		t.Fatalf("err %v, want unknown_source", err)
	}
}

func TestTooManySourcesAreRefused(t *testing.T) {
	s, _ := open(t)
	var edits []Edit
	for i := 0; i <= MaxSources; i++ {
		edits = append(edits, Edit{Address: "/var/run/app" + strings.Repeat("x", i) + ".sock"})
	}
	_, err := s.Replace(edits)
	var ae *AddressError
	if !errors.As(err, &ae) || ae.Code != "too_many" {
		t.Fatalf("err %v, want too_many", err)
	}
}

func TestARefusedEditChangesNothing(t *testing.T) {
	s, _ := open(t)
	_, _ = s.Replace([]Edit{{Address: "/var/run/a.sock"}})
	_, _ = s.Replace([]Edit{{Address: "nonsense"}})
	if list := s.List(); len(list) != 1 || list[0].Address != "/var/run/a.sock" {
		t.Fatalf("list %+v", list)
	}
}

func TestALearnedPinIsKept(t *testing.T) {
	s, path := open(t)
	pub, _ := s.Replace([]Edit{{Address: "https://tower.lan:3443", Token: "t"}})
	s.Learn(pub[0].ID, "https://tower.lan:3443", "ab12")
	again, _ := Open(path, "")
	if got := again.List()[0].Pin; got != "ab12" {
		t.Fatalf("pin %q", got)
	}
}

func TestLearningNeverOverwritesAPin(t *testing.T) {
	s, _ := open(t)
	pub, _ := s.Replace([]Edit{{Address: "https://tower.lan:3443", Token: "t"}})
	s.Learn(pub[0].ID, "https://tower.lan:3443", "first")
	s.Learn(pub[0].ID, "https://tower.lan:3443", "second")
	if got := s.List()[0].Pin; got != "first" {
		t.Fatalf("pin %q, want first", got)
	}
}

func TestANewAddressForgetsThePin(t *testing.T) {
	s, _ := open(t)
	pub, _ := s.Replace([]Edit{{Address: "https://tower.lan:3443", Token: "t"}})
	s.Learn(pub[0].ID, "https://tower.lan:3443", "ab12")
	_, _ = s.Replace([]Edit{{ID: pub[0].ID, Address: "https://other.lan:3443", Token: "new"}})
	if got := s.List()[0].Pin; got != "" {
		t.Fatalf("pin %q survived a new address", got)
	}
}

func TestTrustForgetsThePin(t *testing.T) {
	s, _ := open(t)
	pub, _ := s.Replace([]Edit{{Address: "https://tower.lan:3443", Token: "t"}})
	s.Learn(pub[0].ID, "https://tower.lan:3443", "ab12")
	if !s.List()[0].Pinned() {
		t.Fatal("no pin after learning")
	}
	_, _ = s.Replace([]Edit{{ID: pub[0].ID, Address: "https://tower.lan:3443", Trust: true}})
	if got := s.List()[0].Pin; got != "" {
		t.Fatalf("pin %q survived trust", got)
	}
}

func TestTheFileIsReadableByRootOnly(t *testing.T) {
	s, path := open(t)
	_, _ = s.Replace([]Edit{{Address: "https://tower.lan:3443", Token: "t"}})
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if isUnixModes() && fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o, want 600", fi.Mode().Perm())
	}
}

func TestOldWidgetSettingsBecomeASource(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "widget.cfg")
	if err := os.WriteFile(legacy, []byte("BV_URL=\"https://192.168.1.10:3443/\"\nWIDGET_TOKEN=\"abc123\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "sources.json"), legacy)
	if err != nil {
		t.Fatal(err)
	}
	list := s.List()
	if len(list) != 1 || list[0].Address != "https://192.168.1.10:3443" || list[0].Token != "abc123" {
		t.Fatalf("list %+v", list)
	}
}

func TestAWidgetURLInTheOldTokenFieldGivesItsToken(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "widget.cfg")
	_ = os.WriteFile(legacy, []byte("BV_URL=\"https://192.168.1.10:3443\"\nWIDGET_TOKEN=\"https://192.168.1.10:3443/widget?token=abc%2B123&x=1\"\n"), 0o600)
	s, err := Open(filepath.Join(dir, "sources.json"), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0].Token; got != "abc+123" {
		t.Fatalf("token %q", got)
	}
}

func TestOldWidgetSettingsAreTakenOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "widget.cfg")
	_ = os.WriteFile(legacy, []byte("BV_URL=\"https://192.168.1.10:3443\"\nWIDGET_TOKEN=\"abc\"\n"), 0o600)
	path := filepath.Join(dir, "sources.json")
	s, _ := Open(path, legacy)
	if _, err := s.Replace(nil); err != nil {
		t.Fatal(err)
	}
	again, _ := Open(path, legacy)
	if n := len(again.List()); n != 0 {
		t.Fatalf("removed widget source came back, %d sources", n)
	}
}

func TestIncompleteOldWidgetSettingsAreIgnored(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "widget.cfg")
	_ = os.WriteFile(legacy, []byte("BV_URL=\"\"\nWIDGET_TOKEN=\"abc\"\n"), 0o600)
	s, err := Open(filepath.Join(dir, "sources.json"), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(s.List()); n != 0 {
		t.Fatalf("%d sources from an incomplete cfg", n)
	}
}

func TestABrokenFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.json")
	_ = os.WriteFile(path, []byte("{not json"), 0o600)
	if _, err := Open(path, ""); err == nil {
		t.Fatal("a broken sources file opened without error")
	}
}

func TestAProbeOfTheSavedAddressUsesTheSavedTokenAndPin(t *testing.T) {
	s, _ := open(t)
	pub, _ := s.Replace([]Edit{{Address: "https://tower.lan:3443", Token: "saved"}})
	s.Learn(pub[0].ID, "https://tower.lan:3443", "ab12")
	got, err := Probe(Edit{ID: pub[0].ID, Address: "https://tower.lan:3443/"}, s.List())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != pub[0].ID || got.Token != "saved" || got.Pin != "ab12" {
		t.Fatalf("probe %+v", got)
	}
}

func TestAProbeOfANewAddressCarriesNothingSaved(t *testing.T) {
	s, _ := open(t)
	pub, _ := s.Replace([]Edit{{Address: "https://tower.lan:3443", Token: "saved"}})
	s.Learn(pub[0].ID, "https://tower.lan:3443", "ab12")
	got, err := Probe(Edit{ID: pub[0].ID, Address: "https://other.lan", Token: "typed"}, s.List())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "" || got.Token != "typed" || got.Pin != "" {
		t.Fatalf("probe %+v", got)
	}
}

func TestAProbeNeedsAValidAddressAndToken(t *testing.T) {
	var ae *AddressError
	if _, err := Probe(Edit{Address: "ftp://x"}, nil); !errors.As(err, &ae) || ae.Code != "invalid_address" {
		t.Errorf("err %v", err)
	}
	if _, err := Probe(Edit{Address: "https://x.lan"}, nil); !errors.As(err, &ae) || ae.Code != "token_missing" {
		t.Errorf("err %v", err)
	}
	if _, err := Probe(Edit{Address: "/var/run/x.sock"}, nil); err != nil {
		t.Errorf("socket: %v", err)
	}
}

func TestANewAddressNeedsTheTokenAgain(t *testing.T) {
	s, _ := open(t)
	pub, _ := s.Replace([]Edit{{Address: "https://tower.lan:3443", Token: "saved"}})
	_, err := s.Replace([]Edit{{ID: pub[0].ID, Address: "https://other.lan:3443"}})
	var ae *AddressError
	if !errors.As(err, &ae) || ae.Code != "token_missing" {
		t.Fatalf("err %v, want token_missing", err)
	}
	if got := s.List()[0].Token; got != "saved" {
		t.Fatalf("token %q after a refused edit", got)
	}
}

func TestAPinLearnedForAnOldAddressIsDropped(t *testing.T) {
	s, _ := open(t)
	pub, _ := s.Replace([]Edit{{Address: "https://tower.lan:3443", Token: "t"}})
	_, _ = s.Replace([]Edit{{ID: pub[0].ID, Address: "https://other.lan:3443", Token: "t"}})
	s.Learn(pub[0].ID, "https://tower.lan:3443", "old")
	if got := s.List()[0].Pin; got != "" {
		t.Fatalf("pin %q of the old address landed on the new one", got)
	}
}
