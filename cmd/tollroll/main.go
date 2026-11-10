// Command tollroll is the service behind the TollRoll tile. It reads the
// status contract of every configured app and answers the plugin's PHP proxy
// on a unix socket. It opens no TCP port.
package main

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/junkerderprovinz/tollroll/internal/api"
	"github.com/junkerderprovinz/tollroll/internal/board"
	"github.com/junkerderprovinz/tollroll/internal/fetch"
	"github.com/junkerderprovinz/tollroll/internal/sources"
)

type config struct {
	Socket  string
	Sources string
	Legacy  string
	SockDir string
}

func configFromEnv() config {
	return config{
		Socket:  cmp.Or(os.Getenv("SOCKET"), "/var/run/tollroll.sock"),
		Sources: cmp.Or(os.Getenv("SOURCES"), "/boot/config/plugins/tollroll/sources.json"),
		Legacy:  os.Getenv("LEGACY_CFG"),
		SockDir: cmp.Or(os.Getenv("SOCK_DIR"), "/var/run"),
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, configFromEnv()); err != nil {
		slog.Error("tollroll stopped", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config) error {
	store, err := sources.Open(cfg.Sources, cfg.Legacy)
	if err != nil {
		return err
	}
	reader := fetch.New(store.Learn)
	b := board.New(store.List, reader)
	ln, err := listen(cfg.Socket)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           api.New(store, b, reader, cfg.SockDir),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	slog.Info("tollroll listening", "socket", cfg.Socket, "sources", len(store.List()))
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// listen opens the socket for root and its group, which is what the WebGUI's
// PHP runs as. A socket file left by an unclean stop is replaced.
func listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}
