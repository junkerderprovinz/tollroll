// Package api serves the tile and the settings page. It listens only on the
// service's unix socket, so the one way in is the PHP proxy behind the
// Unraid login. No answer ever carries a secret.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"

	"github.com/junkerderprovinz/parleyport/kit/notify"

	"github.com/junkerderprovinz/tollroll/internal/alert"
	"github.com/junkerderprovinz/tollroll/internal/board"
	"github.com/junkerderprovinz/tollroll/internal/channels"
	"github.com/junkerderprovinz/tollroll/internal/fortress"
	"github.com/junkerderprovinz/tollroll/internal/prefs"
)

const maxBody = 64 << 10

var langTag = regexp.MustCompile(`^[a-zA-Z]{2,3}([-_][a-zA-Z0-9]{2,8})?$`)

type handler struct {
	board    *board.Board
	channels *channels.Store
	texts    func() (string, alert.Texts)
	fortress Fortress
	prefs    *prefs.Store
}

// Fortress is the tile's membership in a fortress.
type Fortress interface {
	Map() fortress.Map
	Join(words string) error
	Leave() error
	Remove(id string) error
}

// New returns the handler. texts gives the words of a channel's test message.
func New(b *board.Board, ch *channels.Store, texts func() (string, alert.Texts), fort Fortress, p *prefs.Store) http.Handler {
	h := &handler{board: b, channels: ch, texts: texts, fortress: fort, prefs: p}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/board", h.view)
	mux.HandleFunc("GET /api/icons", h.icons)
	mux.HandleFunc("GET /api/notify", h.listChannels)
	mux.HandleFunc("PUT /api/notify", h.replaceChannels)
	mux.HandleFunc("POST /api/notify/test", h.testChannel)
	mux.HandleFunc("GET /api/fortress", h.fortressMap)
	mux.HandleFunc("POST /api/fortress/join", h.join)
	mux.HandleFunc("POST /api/fortress/leave", h.leave)
	mux.HandleFunc("POST /api/fortress/remove", h.remove)
	mux.HandleFunc("GET /api/tile", h.tile)
	mux.HandleFunc("PUT /api/tile", h.setTile)
	return mux
}

func (h *handler) tile(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.prefs.Get())
}

func (h *handler) setTile(w http.ResponseWriter, r *http.Request) {
	var body prefs.Prefs
	if !readJSON(w, r, &body) {
		return
	}
	err := h.prefs.Set(body)
	switch {
	case errors.Is(err, prefs.ErrView):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_view"})
	case errors.Is(err, prefs.ErrHidden):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_hidden"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save_failed"})
	default:
		writeJSON(w, http.StatusOK, h.prefs.Get())
	}
}

func (h *handler) fortressMap(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.fortress.Map())
}

// join takes the tile into the fortress of the phrase it is given. The
// phrase goes no further than the membership, which keeps only the keys.
func (h *handler) join(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phrase string `json:"phrase"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	err := h.fortress.Join(body.Phrase)
	switch {
	case errors.Is(err, fortress.ErrPhrase):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "phrase_invalid"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "join_failed"})
	default:
		writeJSON(w, http.StatusOK, h.fortress.Map())
	}
}

func (h *handler) leave(w http.ResponseWriter, _ *http.Request) {
	if err := h.fortress.Leave(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "leave_failed"})
		return
	}
	writeJSON(w, http.StatusOK, h.fortress.Map())
}

// remove takes another member out of the fortress. The answer is the map,
// which names it until the phrase changes.
func (h *handler) remove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	err := h.fortress.Remove(body.ID)
	switch {
	case errors.Is(err, fortress.ErrNotJoined):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "not_joined"})
	case errors.Is(err, fortress.ErrNoMember):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no_member"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "remove_failed"})
	default:
		writeJSON(w, http.StatusOK, h.fortress.Map())
	}
}

func (h *handler) view(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	if !langTag.MatchString(lang) {
		lang = ""
	}
	writeJSON(w, http.StatusOK, h.board.View(r.Context(), lang))
}

func (h *handler) icons(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"icons": h.board.Icons()})
}

func (h *handler) listChannels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"channels": h.channels.Public()})
}

func (h *handler) replaceChannels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Channels []notify.Channel `json:"channels"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	pub, err := h.channels.Replace(body.Channels)
	var ps notify.Problems
	switch {
	case errors.As(err, &ps):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid", "problems": ps})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save_failed"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"channels": pub})
	}
}

// testChannel sends the test message to one channel as the settings page has
// it. A channel takes its saved secrets along only to its saved address.
func (h *handler) testChannel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Channel notify.Channel `json:"channel"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	_, tx := h.texts()
	res := h.channels.Test(r.Context(), body.Channel, notify.Event{
		Kind: "test", Severity: notify.SeverityInfo, Title: tx.Get("n_test_title", ""), Body: tx.Get("n_test_body", ""),
	})
	if !res.OK() {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": res.Code})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
