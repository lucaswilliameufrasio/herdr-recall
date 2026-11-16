//go:build linux || darwin

package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStateLockHonorsCancellation(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lockState(context.Background(), dir, ".spool.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := lockState(ctx, dir, ".spool.lock"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("esperava cancelamento: %v", err)
	}
}
