package channels

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/notify"
)

// unraidGap keeps Unraid notifications a second apart: Unraid names each one
// after its sender and the second it arrives, and quietly drops a second
// notification of the same second.
const unraidGap = time.Second

// spaced runs one program at a time, at least gap after the one before.
type spaced struct {
	next notify.Runner
	gap  time.Duration

	mu   sync.Mutex
	last time.Time
}

func (s *spaced) Run(ctx context.Context, name string, args ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if wait := time.Until(s.last.Add(s.gap)); wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	defer func() { s.last = time.Now() }()
	return s.next.Run(ctx, name, args...)
}

// local runs the program on this machine, as the service does on the host.
type local struct{}

func (local) Run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		if said := strings.TrimSpace(string(out[:min(len(out), 200)])); said != "" {
			return fmt.Errorf("%w: %s", err, said)
		}
	}
	return err
}
