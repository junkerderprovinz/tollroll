package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/sources"
)

const token = "read-token"

type app struct {
	mu   sync.Mutex
	lang string
	q    status.LogQuery
}

func (a *app) Report(_ context.Context, lang string) (status.Report, error) {
	a.mu.Lock()
	a.lang = lang
	a.mu.Unlock()
	return status.Report{
		App:      status.App{ID: "example", Name: "Example", Version: "1.2.3"},
		Instance: status.Instance{ID: "i1", Name: "Tower"},
		State:    status.StateOK,
	}, nil
}

func (a *app) Log(_ context.Context, q status.LogQuery) (status.LogPage, error) {
	a.mu.Lock()
	a.q = q
	a.mu.Unlock()
	return status.LogPage{
		Entries: []status.LogEntry{{ID: "7", At: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), Level: status.SeverityInfo, Code: "run.done", Text: "done"}},
		Next:    "7",
	}, nil
}

func bearer(r *http.Request) error {
	switch r.Header.Get("Authorization") {
	case "Bearer " + token:
		return nil
	case "":
		return status.ErrUnauthenticated
	}
	return status.ErrForbidden
}

func tlsApp(t *testing.T, a *app) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(status.Handler(a, bearer))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(srv.Certificate().Raw)
	return srv, hex.EncodeToString(sum[:])
}

type learned struct {
	mu   sync.Mutex
	pins map[string]string
}

func (l *learned) learn(id, _, pin string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pins == nil {
		l.pins = map[string]string{}
	}
	l.pins[id] = pin
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestAReportComesBackOverHTTPSWithTheToken(t *testing.T) {
	a := &app{}
	srv, _ := tlsApp(t, a)
	c := New(nil)
	rep, err := c.Report(context.Background(), sources.Source{ID: "s1", Address: srv.URL, Token: token}, "de")
	if err != nil {
		t.Fatal(err)
	}
	if rep.App.Name != "Example" || rep.State != status.StateOK {
		t.Fatalf("report %+v", rep)
	}
	if a.lang != "de" {
		t.Fatalf("app was asked for %q, want de", a.lang)
	}
}

func TestTheFirstCertificateIsLearned(t *testing.T) {
	srv, pin := tlsApp(t, &app{})
	l := &learned{}
	c := New(l.learn)
	if _, err := c.Report(context.Background(), sources.Source{ID: "s1", Address: srv.URL, Token: token}, ""); err != nil {
		t.Fatal(err)
	}
	if l.pins["s1"] != pin {
		t.Fatalf("learned %q, want %q", l.pins["s1"], pin)
	}
}

func TestALearnedCertificateIsAccepted(t *testing.T) {
	srv, pin := tlsApp(t, &app{})
	l := &learned{}
	c := New(l.learn)
	if _, err := c.Report(context.Background(), sources.Source{ID: "s1", Address: srv.URL, Token: token, Pin: pin}, ""); err != nil {
		t.Fatal(err)
	}
	if len(l.pins) != 0 {
		t.Fatalf("learned again: %v", l.pins)
	}
}

func TestAnotherCertificateIsRefused(t *testing.T) {
	srv, _ := tlsApp(t, &app{})
	c := New(nil)
	_, err := c.Report(context.Background(), sources.Source{ID: "s1", Address: srv.URL, Token: token, Pin: strings.Repeat("0", 64)}, "")
	if code(err) != "cert_changed" {
		t.Fatalf("err %v, want cert_changed", err)
	}
}

func TestACertificateTheSystemTrustsNeedsNoPin(t *testing.T) {
	srv, _ := tlsApp(t, &app{})
	l := &learned{}
	c := New(l.learn)
	c.roots = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	src := sources.Source{ID: "s1", Address: strings.Replace(srv.URL, "127.0.0.1", "example.com", 1), Token: token, Pin: strings.Repeat("0", 64)}
	c.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Listener.Addr().String())
	}
	if _, err := c.Report(context.Background(), src, ""); err != nil {
		t.Fatal(err)
	}
	if len(l.pins) != 0 {
		t.Fatalf("pinned a certificate the system trusts: %v", l.pins)
	}
}

func TestAMissingTokenIsUnauthorized(t *testing.T) {
	srv, _ := tlsApp(t, &app{})
	_, err := New(nil).Report(context.Background(), sources.Source{ID: "s1", Address: srv.URL}, "")
	if code(err) != "unauthorized" {
		t.Fatalf("err %v, want unauthorized", err)
	}
}

func TestAWrongTokenIsForbidden(t *testing.T) {
	srv, _ := tlsApp(t, &app{})
	_, err := New(nil).Report(context.Background(), sources.Source{ID: "s1", Address: srv.URL, Token: "other"}, "")
	if code(err) != "forbidden" {
		t.Fatalf("err %v, want forbidden", err)
	}
}

func plain(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestAnAppWithoutTheContractIsNotFound(t *testing.T) {
	u := plain(t, func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })
	_, err := New(nil).Report(context.Background(), sources.Source{Address: u, Token: token}, "")
	if code(err) != "not_found" {
		t.Fatalf("err %v, want not_found", err)
	}
}

func TestAnAnswerThatIsNoReportIsBad(t *testing.T) {
	for name, body := range map[string]string{
		"html":     "<html>login</html>",
		"contract": `{"contract":2,"app":{"id":"x","name":"X","version":"1"},"state":"ok"}`,
		"no app":   `{"contract":1,"app":{"id":"","name":"","version":""},"state":"ok"}`,
	} {
		u := plain(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
		_, err := New(nil).Report(context.Background(), sources.Source{Address: u, Token: token}, "")
		if code(err) != "bad_answer" {
			t.Errorf("%s: err %v, want bad_answer", name, err)
		}
	}
}

func TestARedirectIsNotFollowed(t *testing.T) {
	target := plain(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect followed to %s with %q", r.URL, r.Header.Get("Authorization"))
	})
	u := plain(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target, http.StatusFound) })
	_, err := New(nil).Report(context.Background(), sources.Source{Address: u, Token: token}, "")
	if code(err) != "bad_answer" {
		t.Fatalf("err %v, want bad_answer", err)
	}
}

func TestAnOversizedAnswerIsBad(t *testing.T) {
	u := plain(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"contract":1,"app":{"id":"x","name":"` + strings.Repeat("x", 2<<20) + `"}}`))
	})
	_, err := New(nil).Report(context.Background(), sources.Source{Address: u, Token: token}, "")
	if code(err) != "bad_answer" {
		t.Fatalf("err %v, want bad_answer", err)
	}
}

func TestAClosedPortIsUnreachable(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	_, err := New(nil).Report(context.Background(), sources.Source{Address: "http://" + addr, Token: token}, "")
	if code(err) != "unreachable" {
		t.Fatalf("err %v, want unreachable", err)
	}
}

func TestAPathInTheAddressIsKept(t *testing.T) {
	var got string
	u := plain(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		http.NotFound(w, r)
	})
	_, _ = New(nil).Report(context.Background(), sources.Source{Address: u + "/vault", Token: token}, "")
	if got != "/vault"+status.StatusPath {
		t.Fatalf("asked for %q", got)
	}
}

func TestTheLogIsReadFromTheCursor(t *testing.T) {
	a := &app{}
	srv, _ := tlsApp(t, a)
	page, err := New(nil).Log(context.Background(), sources.Source{Address: srv.URL, Token: token}, "6", "fr", 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Next != "7" {
		t.Fatalf("page %+v", page)
	}
	if a.q.After != "6" || a.q.Lang != "fr" || a.q.Limit != 200 {
		t.Fatalf("query %+v", a.q)
	}
}

func TestASocketIsReadWithoutAToken(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "app.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("no unix sockets here: %v", err)
	}
	var auth string
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: status.Handler(&app{}, func(r *http.Request) error {
		auth = r.Header.Get("Authorization")
		return nil
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	rep, err := New(nil).Report(context.Background(), sources.Source{Address: sock}, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.App.ID != "example" || auth != "" {
		t.Fatalf("report %+v, authorization %q", rep, auth)
	}
}

func TestAnAppTheSystemTrustedNeverFallsBackToFirstUse(t *testing.T) {
	trusted, _ := tlsApp(t, &app{})
	l := &learned{}
	c := New(l.learn)
	c.roots = trusted.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	if _, err := c.Report(context.Background(), sources.Source{ID: "s1", Address: trusted.URL, Token: token}, ""); err != nil {
		t.Fatal(err)
	}
	if l.pins["s1"] != PinSystem {
		t.Fatalf("learned %q, want %q", l.pins["s1"], PinSystem)
	}
	impostor, _ := tlsApp(t, &app{})
	c.roots = nil
	_, err := c.Report(context.Background(), sources.Source{ID: "s1", Address: impostor.URL, Token: token, Pin: PinSystem}, "")
	if code(err) != "cert_changed" {
		t.Fatalf("err %v, want cert_changed", err)
	}
}
