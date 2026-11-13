package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"herdr-recall/internal/contract"
)

func TestLatestPrintsTheDeskWithoutTheServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "latest.json")
	body, err := contract.MarshalSnapshot(contract.Snapshot{
		SnapshotID: "0123456789abcdef",
		Host:       "desk",
		BootID:     "boot-1",
		CapturedAt: "2026-11-13T00:00:00.000000000Z",
		Panes: []contract.Pane{
			{
				WorkspaceID:    "w1",
				WorkspaceLabel: "mesa",
				TabID:          "t1",
				TabLabel:       "aba",
				PaneID:         "p0",
				PaneLabel:      "solto",
				Cwd:            "/tmp/a<b",
				MatchBasis:     contract.MatchNone,
				Confidence:     contract.ConfidenceLow,
			},
			{
				WorkspaceID:    "w1",
				WorkspaceLabel: "mesa",
				TabID:          "t1",
				TabLabel:       "aba",
				PaneID:         "p1",
				PaneLabel:      "pane",
				Cwd:            "/home/project",
				HerdrSession:   "ses_abcdef12",
				Harness:        contract.HarnessOpenCode,
				SessionID:      "ses_abcdef12",
				ResumeArgv:     []string{"opencode", "--session", "ses_abcdef12"},
				MatchBasis:     contract.MatchHerdrAgentSession,
				Confidence:     contract.ConfidenceHigh,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, log bytes.Buffer
	if err := run([]string{"latest"}, &out, &log, path); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"workspace", "aba", "pane", "diretório", "harness", "sessão", "confiança", "comando",
		"mesa", "solto", "/tmp/a<b", "baixa", "—",
		"opencode", "ses_abcdef12", "alta",
		"'opencode' '--session' 'ses_abcdef12'",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("faltou %q em %s", want, text)
		}
	}
	if strings.Contains(text, "high") || strings.Contains(text, "low") {
		t.Fatalf("confiança em inglês: %s", text)
	}
	logged := log.String()
	if logged != "latest panes=2\n" {
		t.Fatalf("log: %q", logged)
	}
	for _, secret := range []string{"ses_abcdef12", "/home/project", "/tmp/a<b", "opencode", "--session", "argv"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("log contém %q: %s", secret, logged)
		}
	}
}

func TestLatestWithoutADesk(t *testing.T) {
	var out, log bytes.Buffer
	if err := run([]string{"latest"}, &out, &log, filepath.Join(t.TempDir(), "latest.json")); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Nenhum pane nesta captura.\n" {
		t.Fatalf("stdout: %q", out.String())
	}
	if strings.Contains(log.String(), "opencode") || strings.Contains(log.String(), "--session") {
		t.Fatalf("log: %q", log.String())
	}
}

func TestRunWithoutLatestStaysLocal(t *testing.T) {
	var out, log bytes.Buffer
	if err := run(nil, &out, &log, ""); err != nil {
		t.Fatal(err)
	}
	if out.String() != "herdr-recall\n" {
		t.Fatalf("stdout: %q", out.String())
	}
	if log.Len() != 0 {
		t.Fatalf("log: %q", log.String())
	}
}
