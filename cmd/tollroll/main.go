// Command tollroll is the service behind the TollRoll tile. It reads the
// status contract of every configured app and of every member of the
// fortress it joined, and answers the plugin's PHP proxy on a unix socket.
// It opens no TCP port.
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

	"github.com/junkerderprovinz/parleyport/kit/notify"

	"github.com/junkerderprovinz/tollroll/internal/alert"
	"github.com/junkerderprovinz/tollroll/internal/api"
	"github.com/junkerderprovinz/tollroll/internal/board"
	"github.com/junkerderprovinz/tollroll/internal/channels"
	"github.com/junkerderprovinz/tollroll/internal/fetch"
	"github.com/junkerderprovinz/tollroll/internal/fortress"
	"github.com/junkerderprovinz/tollroll/internal/prefs"
	"github.com/junkerderprovinz/tollroll/internal/sources"
)

type config struct {
	Socket   string
	Sources  string
	Legacy   string
	SockDir  string
	Notify   string
	LangDir  string
	Dynamix  string
	Fortress string
	Tile     string
}

// version is set at build time.
var version = "dev"

func configFromEnv() config {
	return config{
		Socket:   cmp.Or(os.Getenv("SOCKET"), "/var/run/tollroll.sock"),
		Sources:  cmp.Or(os.Getenv("SOURCES"), "/boot/config/plugins/tollroll/sources.json"),
		Legacy:   os.Getenv("LEGACY_CFG"),
		SockDir:  cmp.Or(os.Getenv("SOCK_DIR"), "/var/run"),
		Notify:   cmp.Or(os.Getenv("NOTIFY"), "/boot/config/plugins/tollroll/notify.json"),
		LangDir:  cmp.Or(os.Getenv("LANG_DIR"), "/usr/local/emhttp/plugins/tollroll/lang"),
		Dynamix:  cmp.Or(os.Getenv("DYNAMIX_CFG"), "/boot/config/plugins/dynamix/dynamix.cfg"),
		Fortress: cmp.Or(os.Getenv("FORTRESS"), "/boot/config/plugins/tollroll/fortress"),
		Tile:     cmp.Or(os.Getenv("TILE"), "/boot/config/plugins/tollroll/tile.json"),
	}
}

// checkEvery is how often the apps are read for notifications while a
// channel is on, with or without anyone looking at the tile.
const checkEvery = time.Minute

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
	ch, err := channels.Open(cfg.Notify, notify.Options{})
	if err != nil {
		return err
	}
	device, _ := os.Hostname()
	fort, err := fortress.Open(fortress.Options{Dir: cfg.Fortress, Device: device, Version: version, LAN: true})
	if err != nil {
		return err
	}
	defer func() { _ = fort.Close() }()
	reader := fort.Reader(fetch.New(store.Learn))
	list := func() []sources.Source { return append(store.List(), fort.Sources()...) }
	b := board.New(list, reader)
	texts := func() (string, alert.Texts) {
		lang := alert.Language(cfg.Dynamix)
		return lang, alert.LoadTexts(cfg.LangDir, lang)
	}
	ln, err := listen(cfg.Socket)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           api.New(store, b, reader, cfg.SockDir, ch, texts, fort, prefs.Open(cfg.Tile)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	w := &alert.Watcher{Every: checkEvery, Check: b.Check, Channels: ch, Texts: texts}
	go w.Run(ctx)
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
