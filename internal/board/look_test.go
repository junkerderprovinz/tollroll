package board

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/junkerderprovinz/parleyport/kit/status"
)

func svgIcon(body string) string {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8">` + body + `</svg>`
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
}

func withLook(rep status.Report, color, icon string) status.Report {
	rep.App.Color, rep.App.Icon = color, icon
	return rep
}

func TestAnAppShowsItsOwnColourAndIcon(t *testing.T) {
	w := newWorld(t)
	icon := svgIcon(`<circle r="4"/>`)
	w.add("a", &fakeApp{rep: withLook(report("alpha", "Alpha", "1.0.0", status.StateOK), "#2e7d32", icon)})
	a := w.view().Apps[0]
	if a.Color != "#2e7d32" {
		t.Errorf("color %q", a.Color)
	}
	if a.Icon == "" || len(a.Icon) > 16 {
		t.Fatalf("icon id %q, want a short id", a.Icon)
	}
	if got := w.board.Icons()[a.Icon]; got != icon {
		t.Errorf("Icons()[%q] = %.40q, want the app's data URI", a.Icon, got)
	}
}

func TestALookOutsideTheContractIsDropped(t *testing.T) {
	w := newWorld(t)
	for i, look := range [][2]string{
		{"red", svgIcon(`<script>alert(1)</script>`)},
		{"#2e7d32;background:url(x)", "https://a.lan/icon.svg"},
		{"#2E7D32", "data:text/html;base64,PHNjcmlwdD4="},
	} {
		addr := string(rune('a' + i))
		w.add(addr, &fakeApp{rep: withLook(report(string(rune('a'+i))+"pp", "App", "1.0.0", status.StateOK), look[0], look[1])})
	}
	v := w.view()
	for _, a := range v.Apps {
		if a.Icon != "" {
			t.Errorf("%s: icon %q", a.Key, a.Icon)
		}
	}
	if v.Apps[0].Color != "" || v.Apps[1].Color != "" || v.Apps[2].Color != "#2E7D32" {
		t.Errorf("colors %q %q %q", v.Apps[0].Color, v.Apps[1].Color, v.Apps[2].Color)
	}
	if n := len(w.board.Icons()); n != 0 {
		t.Errorf("%d icons", n)
	}
}

func TestARowTakesTheLookOfTheFirstInstanceThatHasOne(t *testing.T) {
	w := newWorld(t)
	icon := svgIcon(`<rect width="8" height="8"/>`)
	w.add("a", &fakeApp{rep: report("alpha", "Alpha", "1.0.0", status.StateOK)})
	w.add("b", &fakeApp{rep: withLook(report("alpha", "Alpha", "1.0.0", status.StateOK), "#123456", icon)})
	w.add("c", &fakeApp{rep: withLook(report("alpha", "Alpha", "1.0.0", status.StateOK), "#654321", svgIcon(`<circle r="1"/>`))})
	a := w.view().Apps[0]
	if a.Color != "#123456" || w.board.Icons()[a.Icon] != icon {
		t.Errorf("row %+v", a)
	}
}

func TestIconsOfRemovedSourcesAreForgotten(t *testing.T) {
	w := newWorld(t)
	w.add("a", &fakeApp{rep: withLook(report("alpha", "Alpha", "1.0.0", status.StateOK), "", svgIcon(`<circle r="4"/>`))})
	w.view()
	w.srcs = nil
	w.later()
	w.view()
	if n := len(w.board.Icons()); n != 0 {
		t.Errorf("%d icons after the source left", n)
	}
}

func TestCheckSeesEverySourceWithAllItsNeeds(t *testing.T) {
	w := newWorld(t)
	rep := report("alpha", "Alpha", "1.0.0", status.StateWarning)
	rep.App.UI = "https://a.lan/"
	rep.NeedsYou = []status.Need{
		{ID: "n1", Severity: status.SeverityWarn, Text: "the copy is old"},
		{ID: "n2", Severity: status.SeverityInfo, Text: "an update waits"},
	}
	a := &fakeApp{rep: rep}
	w.add("a", a)
	w.add("gone", nil)

	seen := w.board.Check(context.Background(), "en")
	if len(seen) != 2 {
		t.Fatalf("seen %+v", seen)
	}
	up, down := seen[0], seen[1]
	if up.Source != "a" || up.Name != "Alpha" || up.Down || len(up.Needs) != 2 {
		t.Errorf("up %+v", up)
	}
	if down.Source != "gone" || !down.Down || down.Name != "app on gone" || len(down.Needs) != 0 {
		t.Errorf("down %+v", down)
	}

	a.err = errUnreachable
	w.later()
	again := w.board.Check(context.Background(), "en")[0]
	if !again.Down || again.Name != "Alpha" || len(again.Needs) != 2 {
		t.Errorf("an app that stops answering: %+v, want it down with its name and last needs", again)
	}
}
