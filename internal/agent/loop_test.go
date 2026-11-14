package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRunStopsOnCancelAndHidesTheSession(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	started := time.Now()
	opt := testOptions(dir, herdrRaw(sessionPane), time.Time{})
	opt.Now = time.Time{}
	opt.Herdr = func(context.Context) ([]byte, error) {
		calls++
		if calls >= 2 {
			cancel()
		}
		return herdrRaw(sessionPane), nil
	}
	var log bytes.Buffer
	if err := Run(ctx, opt, 40*time.Millisecond, &log); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("voltas: %d", calls)
	}
	if time.Since(started) < 25*time.Millisecond {
		t.Fatal("não esperou o intervalo")
	}
	text := log.String()
	if !strings.Contains(text, "capture panes=1 layouts=1\n") {
		t.Fatalf("log: %q", text)
	}
	if strings.Contains(text, "ses_abcdef12") || strings.Contains(text, "/home/project") {
		t.Fatalf("log vazou: %q", text)
	}
}

func TestRunKeepsGoingWhenHerdrIsDown(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	opt := testOptions(dir, []byte(emptyHerdr()), time.Time{})
	opt.Now = time.Time{}
	opt.Herdr = func(context.Context) ([]byte, error) {
		calls++
		if calls >= 2 {
			cancel()
		}
		return nil, errors.New("herdr parado")
	}
	var log bytes.Buffer
	if err := Run(ctx, opt, 20*time.Millisecond, &log); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("voltas: %d", calls)
	}
	if !strings.Contains(log.String(), "O Herdr não respondeu. A última captura continua aqui.") {
		t.Fatalf("log: %q", log.String())
	}
}

func TestRunReturnsWhenTheContextIsAlreadyDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	opt := testOptions(t.TempDir(), []byte(emptyHerdr()), time.Time{})
	opt.Herdr = func(context.Context) ([]byte, error) {
		calls++
		return []byte(emptyHerdr()), nil
	}
	if err := Run(ctx, opt, time.Hour, io.Discard); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("voltas: %d", calls)
	}
}
