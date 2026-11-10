// Package api serves the tile and the settings page. It listens only on the
// service's unix socket, so the one way in is the PHP proxy behind the
// Unraid login. No answer ever carries a token.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/junkerderprovinz/tollroll/internal/board"
	"github.com/junkerderprovinz/tollroll/internal/fetch"
	"github.com/junkerderprovinz/tollroll/internal/sources"
)

const maxBody = 64 << 10

var langTag = regexp.MustCompile(`^[a-zA-Z]{2,3}([-_][a-zA-Z0-9]{2,8})?$`)

type handler struct {
	store   *sources.Store
	board   *board.Board
	reader  board.Reader
	sockDir string
}

// New returns the handler. reader serves the connection test and the search
// for apps on the sockets in sockDir.
func New(store *sources.Store, b *board.Board, reader board.Reader, sockDir string) http.Handler {
	h := &handler{store: store, board: b, reader: reader, sockDir: sockDir}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/board", h.view)
	mux.HandleFunc("GET /api/sources", h.list)
	mux.HandleFunc("PUT /api/sources", h.replace)
	mux.HandleFunc("POST /api/test", h.test)
	mux.HandleFunc("GET /api/discover", h.discover)
	return mux
}

func (h *handler) view(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	if !langTag.MatchString(lang) {
		lang = ""
	}
	writeJSON(w, http.StatusOK, h.board.View(r.Context(), lang))
}

type listed struct {
	sources.Public
	Last *board.Outcome `json:"last,omitempty"`
}

func (h *handler) list(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sources": h.withOutcomes(h.store.Public())})
}

func (h *handler) withOutcomes(pub []sources.Public) []listed {
	last := h.board.Outcomes()
	out := make([]listed, 0, len(pub))
	for _, p := range pub {
		l := listed{Public: p}
		if o, ok := last[p.ID]; ok {
			l.Last = &o
		}
		out = append(out, l)
	}
	return out
}

func (h *handler) replace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Sources []sources.Edit `json:"sources"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	pub, err := h.store.Replace(body.Sources)
	var ae *sources.AddressError
	switch {
	case errors.As(err, &ae):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ae.Code, "index": ae.Index})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save_failed"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"sources": h.withOutcomes(pub)})
	}
}

// test reads one app as the settings page has it, saved or not. The saved
// token and certificate are used only while the address is still the saved
// one, so typing a new address never sends the old token there.
func (h *handler) test(w http.ResponseWriter, r *http.Request) {
	var body sources.Edit
	if !readJSON(w, r, &body) {
		return
	}
	probe, err := sources.Probe(body, h.store.List())
	if err != nil {
		var ae *sources.AddressError
		code := "invalid_address"
		if errors.As(err, &ae) {
			code = ae.Code
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": code})
		return
	}
	rep, err := h.reader.Report(r.Context(), probe, "")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": errorCode(err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": rep.App.Name, "version": rep.App.Version})
}

type found struct {
	Address string `json:"address"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// discover asks every socket in the socket folder for a status report and
// lists those that answer and are not in the list yet.
func (h *handler) discover(w http.ResponseWriter, r *http.Request) {
	paths, _ := filepath.Glob(filepath.Join(h.sockDir, "*.sock"))
	added := map[string]bool{}
	for _, src := range h.store.List() {
		added[src.Address] = true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = []found{}
	)
	for _, p := range paths {
		addr := filepath.ToSlash(p)
		if added[addr] {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rep, err := h.reader.Report(ctx, sources.Source{Address: addr}, "")
			if err != nil {
				return
			}
			mu.Lock()
			out = append(out, found{Address: addr, Name: rep.App.Name, Version: rep.App.Version})
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	writeJSON(w, http.StatusOK, map[string]any{"found": out})
}

func errorCode(err error) string {
	var fe *fetch.Error
	if errors.As(err, &fe) {
		return fe.Code
	}
	return "unreachable"
}

func readJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		var me *http.MaxBytesError
		if errors.As(err, &me) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_large"})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		}
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
