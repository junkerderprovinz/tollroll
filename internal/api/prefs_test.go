package api

import "testing"

func TestTheTileKeepsItsView(t *testing.T) {
	e := newEnv(t)
	if code, out := e.do("GET", "/api/tile", ""); code != 200 || out["view"] != "all" {
		t.Fatalf("%d %v", code, out)
	}
	if code, out := e.do("PUT", "/api/tile", `{"view":"log"}`); code != 200 || out["view"] != "log" {
		t.Fatalf("%d %v", code, out)
	}
	if _, out := e.do("GET", "/api/tile", ""); out["view"] != "log" {
		t.Errorf("view %v after saving", out["view"])
	}
	if code, out := e.do("PUT", "/api/tile", `{"view":"sideways"}`); code != 400 || out["error"] != "invalid_view" {
		t.Errorf("%d %v", code, out)
	}
}
