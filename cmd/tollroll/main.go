// Command tollroll is the service behind the TollRoll tile. It joins a
// fortress with its phrase, reads the status contract of every member and
// answers the plugin's PHP proxy on a unix socket. It opens no TCP port.
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

	kit "github.com/junkerderprovinz/parleyport/kit/fortress"
	"github.com/junkerderprovinz/parleyport/kit/notify"
	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/advisor"
	"github.com/junkerderprovinz/tollroll/internal/alert"
	"github.com/junkerderprovinz/tollroll/internal/api"
	"github.com/junkerderprovinz/tollroll/internal/board"
	"github.com/junkerderprovinz/tollroll/internal/channels"
	"github.com/junkerderprovinz/tollroll/internal/fortress"
	"github.com/junkerderprovinz/tollroll/internal/host"
	"github.com/junkerderprovinz/tollroll/internal/hub"
	"github.com/junkerderprovinz/tollroll/internal/journal"
	"github.com/junkerderprovinz/tollroll/internal/plugins"
	"github.com/junkerderprovinz/tollroll/internal/prefs"
)

type config struct {
	Socket   string
	Notify   string
	LangDir  string
	Dynamix  string
	Fortress string
	Tile     string
	Watch    string
	Hub      string
	HubState string
	Advice   string
	Plugins  string
	Nginx    string
}

// version is set at build time.
var version = "dev"

func configFromEnv() config {
	return config{
		Socket:   cmp.Or(os.Getenv("SOCKET"), "/var/run/tollroll.sock"),
		Notify:   cmp.Or(os.Getenv("NOTIFY"), "/boot/config/plugins/tollroll/notify.json"),
		LangDir:  cmp.Or(os.Getenv("LANG_DIR"), "/usr/local/emhttp/plugins/tollroll/lang"),
		Dynamix:  cmp.Or(os.Getenv("DYNAMIX_CFG"), "/boot/config/plugins/dynamix/dynamix.cfg"),
		Fortress: cmp.Or(os.Getenv("FORTRESS"), "/boot/config/plugins/tollroll/fortress"),
		Tile:     cmp.Or(os.Getenv("TILE"), "/boot/config/plugins/tollroll/tile.json"),
		Watch:    cmp.Or(os.Getenv("WATCH"), "/boot/config/plugins/tollroll/watch.json"),
		Hub:      cmp.Or(os.Getenv("HUB"), "/boot/config/plugins/tollroll/hub.json"),
		HubState: cmp.Or(os.Getenv("HUB_STATE"), "/boot/config/plugins/tollroll/hub-state.json"),
		Advice:   cmp.Or(os.Getenv("ADVICE"), "/boot/config/plugins/tollroll/advice.json"),
		Plugins:  cmp.Or(os.Getenv("PLUGINS"), plugins.Dir),
		Nginx:    cmp.Or(os.Getenv("NGINX_INI"), "/var/local/emhttp/nginx.ini"),
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
	ch, err := channels.Open(cfg.Notify, notify.Options{})
	if err != nil {
		return err
	}
	ch.Base = func() string { return channels.DefaultURL(cfg.Nginx) }
	device, _ := os.Hostname()
	texts := func() (string, map[string]string) {
		lang := alert.Language(cfg.Dynamix)
		return lang, alert.LoadTexts(cfg.LangDir, lang)
	}
	j := &journal.Journal{
		App:   status.App{ID: fortress.AppID, Name: "TollRoll", Version: version, Icon: fortress.Icon},
		Every: checkEvery,
		Texts: func(lang string) map[string]string { return alert.LoadTexts(cfg.LangDir, lang) },
	}
	var b *board.Board
	hb := hub.New(hub.Options{Channels: ch, Unraid: ch.Unraid, Settings: hub.OpenStore(cfg.Hub), Texts: texts, Journal: j, State: cfg.HubState,
		Open: func(lang string) []string { return board.OpenLines(b.View(ctx, lang)) }})
	j.Hub = hb.TakesOver
	fort, err := fortress.Open(fortress.Options{Dir: cfg.Fortress, Device: device, Version: version, LAN: true, Journal: j,
		Actions: []kit.Action{hb.Action(fortress.AppName)}})
	if err != nil {
		return err
	}
	defer func() { _ = fort.Close() }()
	go fort.Watch(ctx)
	files := plugins.Files{Dir: cfg.Plugins}
	b = board.New(plugins.Join(fort.Sources, fort.Reader(), files))
	unraid := host.New(host.Options{
		State: cfg.Watch,
		Texts: func(lang string) map[string]string { return alert.LoadTexts(cfg.LangDir, lang) },
		Lang:  func() string { return alert.Language(cfg.Dynamix) },
		Notify: func(ctx context.Context, e notify.Event) {
			for _, r := range hb.Offer(ctx, hub.Message{App: host.Unraid, Name: "Unraid", Key: cmp.Or(e.Fields["key"], e.Kind), Unraid: true, Event: e}) {
				if !r.OK() {
					slog.Warn("notification failed", "channel", r.Channel, "code", r.Code, "err", r.Err)
				}
			}
		},
		Clear:   func(kind string) { hb.Resolve(host.Unraid, kind) },
		Claimed: func(name string) bool { return b.Mentions(ctx, name) },
	})
	b.Local = board.Locals{unraid, j}
	// The host saves what it learned once ctx ends, which the service waits
	// for before it exits.
	hostCtx, stopHost := context.WithCancel(ctx)
	hostDone := make(chan struct{})
	go func() {
		defer close(hostDone)
		unraid.Run(hostCtx)
	}()
	hubDone := make(chan struct{})
	go func() {
		defer close(hubDone)
		hb.Run(hostCtx)
	}()
	defer func() {
		stopHost()
		<-hostDone
		<-hubDone
	}()
	alertTexts := func() (string, alert.Texts) {
		lang, tx := texts()
		return lang, tx
	}
	adv := advisor.Open(cfg.Advice)
	facts := func() advisor.Facts {
		isUnraid, boxes, plugs := unraid.Server()
		f := advisor.Facts{Unraid: isUnraid, Containers: boxes, Plugins: plugs, Backs: b.Doing(status.KindBackup)}
		for _, m := range fort.Map().Members {
			f.Apps = append(f.Apps, m.App)
		}
		for _, s := range files.Sources() {
			f.Apps = append(f.Apps, s.Name)
		}
		return f
	}
	go firstAdvice(ctx, adv, facts, unraid.Advise)
	ln, err := listen(cfg.Socket)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler: api.New(api.Deps{Board: b, Channels: ch, Texts: alertTexts, Fortress: fort, Prefs: prefs.Open(cfg.Tile), Router: unraid.RouterLog, Watch: unraid.Mark, Hub: hb.Settings(),
			Advice: adv, Facts: facts}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	w := &alert.Watcher{Every: checkEvery, Check: b.Check, Channels: hb, Texts: alertTexts, Journal: j}
	go w.Run(ctx)
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	slog.Info("tollroll listening", "socket", cfg.Socket, "joined", fort.Map().Joined)
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// adviseAfter gives the host its first look and the members of the fortress
// time to answer before the advisor looks for the first time.
const adviseAfter = 3 * time.Minute

// firstAdvice puts what the advisor suggests on its first look at this
// server into the server's log, once.
func firstAdvice(ctx context.Context, adv *advisor.Store, facts func() advisor.Facts, note func(key string)) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(adviseAfter):
	}
	for _, s := range adv.First(facts()) {
		note(s.Text)
	}
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
