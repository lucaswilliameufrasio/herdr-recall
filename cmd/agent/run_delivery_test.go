package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"herdr-recall/internal/agent"
)

func TestRunReturnsThreeWhenDeviceIsRevoked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	home := t.TempDir()
	if err := agent.Save(agent.PathsFor(home), server.URL, "synthetic-token", nil); err != nil {
		t.Fatal(err)
	}
	opt := agent.CaptureOptions{
		StateDir: filepath.Join(home, "state"),
		Host:     "host",
		BootID:   "boot-1",
		Now:      time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC),
		GOOS:     "linux",
		Herdr: func(context.Context) ([]byte, error) {
			return []byte(`{"result":{"snapshot":{"workspaces":[],"tabs":[],"panes":[],"layouts":[]}}}`), nil
		},
	}
	var log bytes.Buffer
	if code := runCommand(nil, &log, opt, home); code != 3 {
		t.Fatalf("código de saída: %d", code)
	}
}
