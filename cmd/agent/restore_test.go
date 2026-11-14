package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"herdr-recall/internal/contract"
)

func TestDispatchRestorePlanHidesTheCommandFromTheLog(t *testing.T) {
	home := t.TempDir()
	desk := contract.Snapshot{
		SnapshotID: "0123456789abcdef",
		Host:       "desk",
		BootID:     "boot-1",
		CapturedAt: "2026-11-13T00:00:00.000000000Z",
		Panes: []contract.Pane{{
			WorkspaceID: "w1", WorkspaceLabel: "mesa", TabID: "t1", TabLabel: "aba",
			PaneID: "p1", Cwd: "/home/project",
			HerdrSession: "ses_abcdef12", Harness: contract.HarnessOpenCode, SessionID: "ses_abcdef12",
			ResumeArgv: []string{"opencode", "--session", "ses_abcdef12"},
			MatchBasis: contract.MatchHerdrAgentSession, Confidence: contract.ConfidenceHigh,
		}},
		Layouts: []contract.Layout{{
			WorkspaceID: "w1", TabID: "t1", WorkspaceNumber: 1, TabNumber: 1,
			Root: &contract.Node{Type: contract.NodePane, PaneID: "p1"},
		}},
	}
	raw, err := contract.MarshalSnapshot(desk)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".local", "state", "herdr-recall")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "latest.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	previous := herdrRun
	herdrRun = func(ctx context.Context, argv []string) ([]byte, error) {
		calls = append(calls, append([]string{}, argv...))
		return []byte(`{"result":{"snapshot":{"workspaces":[]}}}`), nil
	}
	t.Cleanup(func() { herdrRun = previous })
	var out, errW bytes.Buffer
	code := dispatch([]string{"restore", "--session", "limpa", "--plan"}, nil, &out, &errW, home, nil, nil, "linux")
	if code != 0 {
		t.Fatalf("saída %d %s", code, errW.String())
	}
	if len(calls) != 1 {
		t.Fatalf("chamadas: %v", calls)
	}
	if !strings.Contains(out.String(), "Nada foi executado.") || !strings.Contains(out.String(), "ses_abcdef12") {
		t.Fatalf("plano: %s", out.String())
	}
	if strings.Contains(errW.String(), "ses_abcdef12") || strings.Contains(errW.String(), "/home/project") {
		t.Fatalf("log: %s", errW.String())
	}
	if !strings.Contains(errW.String(), "restore plan panes=1\n") {
		t.Fatalf("log: %s", errW.String())
	}
}

func TestDispatchRestoreRejectsDefault(t *testing.T) {
	home := t.TempDir()
	var calls int
	previous := herdrRun
	herdrRun = func(context.Context, []string) ([]byte, error) {
		calls++
		return nil, errors.New("não chamar")
	}
	t.Cleanup(func() { herdrRun = previous })
	var out, errW bytes.Buffer
	code := dispatch([]string{"restore", "--session", "default"}, nil, &out, &errW, home, nil, nil, "linux")
	if code != 2 || calls != 0 || !strings.Contains(errW.String(), "default") {
		t.Fatalf("code=%d calls=%d err=%s", code, calls, errW.String())
	}
}

func TestRunHerdrRefusesAnotherCommand(t *testing.T) {
	_, err := runHerdr(context.Background(), []string{"true"})
	if err == nil {
		t.Fatal("aceitou outro comando")
	}
}
