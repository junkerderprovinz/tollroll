// Package channels keeps where TollRoll's notifications go: the channels in
// notify.json, in the format of kit/notify. Secrets stay in this package and
// in the file; the settings page gets the channels through Public, masked, and
// a masked secret it sends back is restored only for an unchanged address.
package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/junkerderprovinz/parleyport/kit/notify"
)

// Source names TollRoll in Unraid's notifications and in Discord footers.
const Source = "TollRoll"

// Store holds the channels and the dispatcher for them. It is safe for
// concurrent use.
type Store struct {
	path string
	opt  notify.Options

	mu   sync.Mutex
	list []notify.Channel
	d    *notify.Dispatcher
}

// Open reads the channels from path. A missing file means no channels; a
// file that does not parse or holds an invalid channel is an error. Unraid
// notifications go through opt.Unraid, or the local notify script without
// one, one at a time.
func Open(path string, opt notify.Options) (*Store, error) {
	opt.Source = Source
	if opt.Unraid == nil {
		opt.Unraid = local{}
	}
	opt.Unraid = &spaced{next: opt.Unraid, gap: unraidGap}
	s := &Store{path: path, opt: opt}
	var list []notify.Channel
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	d, err := notify.NewDispatcher(list, opt)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	s.list, s.d = list, d
	return s, nil
}

// Public returns the channels with every secret masked.
func (s *Store) Public() []notify.Channel {
	s.mu.Lock()
	defer s.mu.Unlock()
	return public(s.list)
}

func public(list []notify.Channel) []notify.Channel {
	out := notify.Redacted(list)
	if out == nil {
		out = []notify.Channel{}
	}
	return out
}

// Replace saves next in place of the channels, after putting back the secrets
// Public masked. A list that does not validate leaves everything as it was
// and comes back as notify.Problems.
func (s *Store) Replace(next []notify.Channel) ([]notify.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := notify.Merge(next, s.list)
	d, err := notify.NewDispatcher(list, s.opt)
	if err != nil {
		return nil, err
	}
	if err := s.save(list); err != nil {
		return nil, err
	}
	s.list, s.d = list, d
	return public(list), nil
}

// Enabled reports whether any channel is switched on.
func (s *Store) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.list {
		if c.Enabled {
			return true
		}
	}
	return false
}

// Send delivers e to every channel that takes it.
func (s *Store) Send(ctx context.Context, e notify.Event) []notify.Result {
	s.mu.Lock()
	d := s.d
	s.mu.Unlock()
	return d.Send(ctx, e)
}

// Test sends e once to ch as the settings page has it, saved or not, with the
// saved secrets of a channel whose address is unchanged.
func (s *Store) Test(ctx context.Context, ch notify.Channel, e notify.Event) notify.Result {
	s.mu.Lock()
	ch = notify.Merge([]notify.Channel{ch}, s.list)[0]
	d := s.d
	s.mu.Unlock()
	return d.Test(ctx, ch, e)
}

func (s *Store) save(list []notify.Channel) error {
	if list == nil {
		list = []notify.Channel{}
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
