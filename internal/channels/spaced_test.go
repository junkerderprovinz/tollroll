package channels

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUnraidNotificationsAreSpacedApart(t *testing.T) {
	inner := &runner{}
	r := &spaced{next: inner, gap: 80 * time.Millisecond}
	start := time.Now()
	for _, name := range []string{"a", "b", "c"} {
		if err := r.Run(context.Background(), name); err != nil {
			t.Fatal(err)
		}
	}
	if took := time.Since(start); took < 160*time.Millisecond {
		t.Errorf("three runs took %s, want two gaps between them", took)
	}
	if len(inner.calls) != 3 {
		t.Errorf("calls %q", inner.calls)
	}
}

func TestAWaitingRunEndsWithItsContext(t *testing.T) {
	inner := &runner{}
	r := &spaced{next: inner, gap: time.Hour}
	if err := r.Run(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := r.Run(ctx, "b"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err %v", err)
	}
	if len(inner.calls) != 1 {
		t.Errorf("calls %q", inner.calls)
	}
}
