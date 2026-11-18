package hub

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	kit "github.com/junkerderprovinz/parleyport/kit/fortress"
	"github.com/junkerderprovinz/parleyport/kit/notify"

	"github.com/junkerderprovinz/tollroll/internal/alert"
)

// DeliverAction is the action through which the apps of the fortress hand
// their notifications to TollRoll, and Capability what TollRoll's status
// report lists while it takes them.
const (
	DeliverAction = "notify.deliver"
	Capability    = "notify.hub"
)

// Delivery is what an app sends with DeliverAction: one notification in the
// app's own words, or with Resolved and its Key alone the word that what the
// key was about is over. URL is another name for Link, the page of the app
// the message leads to.
type Delivery struct {
	Kind     string          `json:"kind"`
	Severity notify.Severity `json:"severity"`
	Title    string          `json:"title"`
	Body     string          `json:"body"`
	Link     string          `json:"link"`
	URL      string          `json:"url"`
	Key      string          `json:"key"`
	Resolved bool            `json:"resolved"`
}

const (
	maxKey  = 100
	maxLink = 500
)

var (
	kind  = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	sevOK = map[notify.Severity]bool{"": true, notify.SeverityInfo: true, notify.SeveritySuccess: true,
		notify.SeverityWarning: true, notify.SeverityFailure: true, notify.SeverityCritical: true}
)

// Action is DeliverAction for the fortress. It takes a delivery while the
// hub takes over and refuses it with hub_off otherwise, so the app sends it
// on its own. name gives an app's name by its ID.
func (h *Hub) Action(name func(app string) string) kit.Action {
	return kit.Action{Name: DeliverAction, Run: func(ctx context.Context, from kit.Caller, body []byte) ([]byte, error) {
		var d Delivery
		if json.Unmarshal(body, &d) != nil || !d.valid() {
			return nil, &kit.Refusal{Status: http.StatusBadRequest, Code: "bad_event"}
		}
		if !h.TakesOver() {
			return nil, &kit.Refusal{Status: http.StatusConflict, Code: "hub_off"}
		}
		app := name(from.App)
		e := notify.Event{Kind: d.Kind, Severity: d.Severity, Title: app + ": " + alert.Plain(d.Title), Body: alert.Plain(d.Body)}
		if link := cmp.Or(d.Link, d.URL); strings.HasPrefix(link, "https://") || strings.HasPrefix(link, "http://") {
			e.Link = link
		}
		// The app waits for the answer only to know that TollRoll took it,
		// so the channels are not kept in its call.
		go h.Deliver(context.WithoutCancel(ctx), Message{App: from.App, Name: app, Key: d.Key, Resolved: d.Resolved, Event: e})
		return []byte(`{"taken":true}`), nil
	}}
}

func (d Delivery) valid() bool {
	link := cmp.Or(d.Link, d.URL)
	if len(d.Key) > maxKey || len(link) > maxLink || !sevOK[d.Severity] || strings.ContainsAny(link, " \"'<>\\") {
		return false
	}
	if d.Resolved {
		return d.Key != ""
	}
	return kind.MatchString(d.Kind) && strings.TrimSpace(d.Title) != ""
}
