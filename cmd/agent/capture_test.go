package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"herdr-recall/internal/agent"
	"herdr-recall/internal/contract"
)

func TestCaptureCommandHidesTheSessionFromTheLog(t *testing.T) {
	dir := t.TempDir()
	var out, errW bytes.Buffer
	code := captureCommand(nil, &out, &errW, agent.CaptureOptions{
		StateDir: dir,
		Host:     "desk",
		BootID:   "boot-1",
		Herdr: func(context.Context) ([]byte, error) {
			return []byte(onePaneHerdr), nil
		},
		NewID: func() (string, error) { return "0123456789abcdef", nil },
	})
	if code != 0 {
		t.Fatalf("code=%d err=%s", code, errW.String())
	}
	if out.String() != "Captura gravada.\n" {
		t.Fatalf("stdout: %q", out.String())
	}
	if errW.String() != "capture panes=1 layouts=1\n" {
		t.Fatalf("log: %q", errW.String())
	}
	for _, secret := range []string{"ses_abcdef12", "/home/project", "opencode", "--session"} {
		if strings.Contains(errW.String(), secret) || strings.Contains(out.String(), secret) {
			t.Fatalf("log vazou %s", secret)
		}
	}
}

func TestCaptureCommandKeepsThePreviousFileWhenHerdrIsDown(t *testing.T) {
	dir := t.TempDir()
	body, err := contract.MarshalSnapshot(contract.Snapshot{
		SnapshotID: "0123456789abcdef",
		Host:       "desk",
		BootID:     "boot-1",
		CapturedAt: "2026-11-13T00:00:00.000000000Z",
		Panes:      []contract.Pane{},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "latest.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errW bytes.Buffer
	code := captureCommand(nil, &out, &errW, agent.CaptureOptions{
		StateDir: dir,
		Host:     "desk",
		BootID:   "boot-1",
		Herdr: func(context.Context) ([]byte, error) {
			return nil, context.DeadlineExceeded
		},
	})
	if code != 1 || out.Len() != 0 {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
	if !strings.Contains(errW.String(), "O Herdr não respondeu") || !strings.Contains(errW.String(), "capture panes=0\n") {
		t.Fatalf("err: %q", errW.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("a falha regravou a captura")
	}
}

func TestCaptureCommandRejectsExtraArguments(t *testing.T) {
	var out, errW bytes.Buffer
	code := captureCommand([]string{"agora"}, &out, &errW, agent.CaptureOptions{})
	if code != 2 || !strings.Contains(errW.String(), "uso: herdr-recall capture") {
		t.Fatalf("code=%d err=%q", code, errW.String())
	}
}

const onePaneHerdr = `{"id":"req-1","result":{"type":"session_snapshot","snapshot":{"version":"t","protocol":22,"workspaces":[{"workspace_id":"w1","label":"mesa","number":1}],"tabs":[{"tab_id":"t1","workspace_id":"w1","label":"aba","number":1}],"panes":[{"pane_id":"p1","workspace_id":"w1","tab_id":"t1","label":"pane","cwd":"/home/project","agent_session":{"agent":"OpenCode","kind":"id","source":"agent","value":"ses_abcdef12"}}],"layouts":[{"workspace_id":"w1","tab_id":"t1","area":{"x":0,"y":0,"width":10,"height":20},"panes":[{"pane_id":"p1","rect":{"x":0,"y":0,"width":10,"height":20}}],"splits":[]}],"agents":[]}}}`
