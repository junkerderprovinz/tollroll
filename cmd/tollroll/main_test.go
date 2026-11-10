package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func socketPath(t *testing.T) string {
	t.Helper()
	// A unix socket path has to stay short, which a nested temp dir may not.
	dir, err := os.MkdirTemp("", "tr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "tollroll.sock")
}

func TestTheServiceAnswersOnItsSocket(t *testing.T) {
	sock := socketPath(t)
	dir := t.TempDir()
	cfg := config{Socket: sock, Sources: filepath.Join(dir, "sources.json"), SockDir: dir,
		Notify: filepath.Join(dir, "notify.json"), LangDir: dir, Dynamix: filepath.Join(dir, "dynamix.cfg")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})

	hc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
	var resp *http.Response
	var err error
	for range 50 {
		if resp, err = hc.Get("http://tollroll/api/board"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("no answer on the socket: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp, err = hc.Get("http://tollroll/api/notify")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("notify: status %d", resp.StatusCode)
	}
}

func TestABrokenChannelFileStopsTheStart(t *testing.T) {
	dir := t.TempDir()
	notify := filepath.Join(dir, "notify.json")
	if err := os.WriteFile(notify, []byte("["), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config{Socket: socketPath(t), Sources: filepath.Join(dir, "sources.json"), SockDir: dir, Notify: notify}
	if err := run(context.Background(), cfg); err == nil {
		t.Fatal("the service started on a channel file it cannot read")
	}
}

func TestAStaleSocketDoesNotBlockTheStart(t *testing.T) {
	sock := socketPath(t)
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := listen(sock)
	if err != nil {
		t.Fatalf("a socket left by an unclean stop blocks the start: %v", err)
	}
	_ = ln.Close()
}

func TestTheSocketIsOpenToRootAndItsGroupOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no unix file modes")
	}
	sock := socketPath(t)
	ln, err := listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o660 {
		t.Fatalf("socket mode %o, want 660", got)
	}
}

func TestSettingsComeFromTheEnvironment(t *testing.T) {
	t.Setenv("SOCKET", "/tmp/x.sock")
	t.Setenv("SOURCES", "")
	cfg := configFromEnv()
	if cfg.Socket != "/tmp/x.sock" || cfg.Sources != "/boot/config/plugins/tollroll/sources.json" ||
		cfg.Legacy != "" || cfg.SockDir != "/var/run" || cfg.Notify != "/boot/config/plugins/tollroll/notify.json" ||
		cfg.LangDir != "/usr/local/emhttp/plugins/tollroll/lang" || cfg.Dynamix != "/boot/config/plugins/dynamix/dynamix.cfg" {
		t.Fatalf("config %+v", cfg)
	}
}
