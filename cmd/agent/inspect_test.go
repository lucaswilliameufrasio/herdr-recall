package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"herdr-recall/internal/agent"
	"herdr-recall/internal/contract"
)

func deskFixture(t *testing.T, id, at string, workspaces ...string) []byte {
	t.Helper()
	snap := contract.Snapshot{SnapshotID: id, Host: "desk", BootID: "boot-1", CapturedAt: at}
	for i, ws := range workspaces {
		pane := contract.Pane{
			WorkspaceID: "w" + ws, WorkspaceLabel: ws, TabID: "t" + ws, TabLabel: "aba",
			PaneID: "p" + string(rune('a'+i)), PaneLabel: "pane", Cwd: "/home/project",
			MatchBasis: contract.MatchNone, Confidence: contract.ConfidenceLow,
		}
		if ws == "api" {
			pane.Harness = contract.HarnessOpenCode
			pane.HerdrSession = "ses_abcdef12"
			pane.SessionID = "ses_abcdef12"
			pane.ResumeArgv = []string{"opencode", "--session", "ses_abcdef12"}
			pane.MatchBasis = contract.MatchHerdrAgentSession
			pane.Confidence = contract.ConfidenceHigh
		}
		snap.Panes = append(snap.Panes, pane)
	}
	body, err := contract.MarshalSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func writeState(t *testing.T, home string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(stateDir(home), name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func fixedNow(t *testing.T, at time.Time) {
	previous := nowFunc
	nowFunc = func() time.Time { return at }
	t.Cleanup(func() { nowFunc = previous })
}

func TestHistoryListsNewestFirstAndShowsByIndex(t *testing.T) {
	home := t.TempDir()
	fixedNow(t, time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC))
	older := deskFixture(t, "aaaaaaaaaaaaaaaa", "2026-11-12T23:00:00.000000000Z", "web")
	newer := deskFixture(t, "bbbbbbbbbbbbbbbb", "2026-11-13T00:30:00.000000000Z", "api", "web")
	writeState(t, home, map[string]string{
		"history/20261112.jsonl": string(older) + "\n",
		"history/20261113.jsonl": string(newer) + "\n" + "não é json\n" + string(newer) + "\n",
	})
	code, out, _ := runCLI(t, home, "history")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 3 || !strings.HasPrefix(lines[1], "1 ") || !strings.Contains(lines[1], "há 30 min") || !strings.Contains(lines[2], "há 2 h") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	code, out, _ = runCLI(t, home, "history", "--json")
	var list []historyJSON
	if code != 0 || json.Unmarshal([]byte(out), &list) != nil || len(list) != 2 || list[0].Resumable != 1 || list[1].SnapshotID != "aaaaaaaaaaaaaaaa" {
		t.Fatalf("json: %q", out)
	}
	code, out, _ = runCLI(t, home, "history", "show", "1", "--workspace", "api")
	if code != 0 || !strings.Contains(out, "'opencode' '--session' 'ses_abcdef12'") || strings.Contains(out, "web") {
		t.Fatalf("show: code=%d out=%q", code, out)
	}
	if code, _, errW := runCLI(t, home, "history", "show", "9"); code != 2 || !strings.Contains(errW, "não tem a captura 9") {
		t.Fatalf("show 9: %d %q", code, errW)
	}
	if code, _, _ := runCLI(t, home, "history", "show", "zero"); code != 2 {
		t.Fatalf("show zero: %d", code)
	}
}

func TestHistoryEmpty(t *testing.T) {
	code, out, _ := runCLI(t, t.TempDir(), "history")
	if code != 0 || out != "Nenhuma captura no histórico.\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestLatestJSONAndFilters(t *testing.T) {
	home := t.TempDir()
	writeState(t, home, map[string]string{"latest.json": string(deskFixture(t, "cccccccccccccccc", "2026-11-13T00:00:00.000000000Z", "api", "web"))})
	code, out, _ := runCLI(t, home, "latest", "--json", "--harness", "OPENCODE")
	var snap contract.Snapshot
	if code != 0 || json.Unmarshal([]byte(out), &snap) != nil || len(snap.Panes) != 1 || snap.Panes[0].WorkspaceLabel != "api" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	code, out, _ = runCLI(t, home, "latest", "-w", "WE")
	if code != 0 || !strings.Contains(out, "web") || strings.Contains(out, "opencode") {
		t.Fatalf("filtro: %q", out)
	}
}

func TestQueueShowsMetadataOnlyAndFailsOnQuarantine(t *testing.T) {
	home := t.TempDir()
	desk := deskFixture(t, "dddddddddddddddd", "2026-11-13T00:00:00.000000000Z", "segredo-do-cwd")
	pending := `{"items":[{"kind":"presence","observed_at":"2026-11-13T00:00:00.000000000Z","body":{"x":1}},{"kind":"snapshot","snapshot_id":"dddddddddddddddd","body":` + string(desk) + `}]}`
	writeState(t, home, map[string]string{"spool.json": pending})
	code, out, _ := runCLI(t, home, "queue")
	if code != 0 || !strings.Contains(out, "Pendentes (2)") || !strings.Contains(out, "1 mesa · 1 presença") || strings.Contains(out, "segredo-do-cwd") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	writeState(t, home, map[string]string{"spool.conflict": pending})
	code, out, _ = runCLI(t, home, "queue", "--json")
	var parsed map[string][]queueItemJSON
	if code != 1 || json.Unmarshal([]byte(out), &parsed) != nil || len(parsed["conflicts"]) != 2 || parsed["pending"][1].ObservedAt == "" {
		t.Fatalf("quarentena: code=%d out=%q", code, out)
	}
	if strings.Contains(out, "segredo-do-cwd") {
		t.Fatal("o conteúdo vazou")
	}
}

func TestStatusSummarizesAndUsesDoctorExitCode(t *testing.T) {
	home := t.TempDir()
	fixedNow(t, time.Date(2026, 11, 13, 0, 5, 0, 0, time.UTC))
	writeState(t, home, map[string]string{"latest.json": string(deskFixture(t, "eeeeeeeeeeeeeeee", "2026-11-13T00:00:00.000000000Z", "api", "web"))})
	checks := []agent.Check{{Name: "unidade", Status: agent.StatusOK, Detail: "instalada"}, {Name: "linger", Status: agent.StatusWarn, Detail: "desligado", Fix: "loginctl enable-linger"}}
	previous := doctorFunc
	doctorFunc = func(context.Context, *env) []agent.Check { return checks }
	t.Cleanup(func() { doctorFunc = previous })
	code, out, _ := runCLI(t, home, "status")
	for _, want := range []string{"não enrolada", "há 5 min", "2 panes em 2 workspaces", "1 retomável", "Fila", "vazia", "→ loginctl enable-linger", "Tudo certo."} {
		if !strings.Contains(out, want) {
			t.Fatalf("falta %q: %q", want, out)
		}
	}
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	checks = append(checks, agent.Check{Name: "servidor", Status: agent.StatusFail, Detail: "fora"})
	code, out, _ = runCLI(t, home, "status", "--json")
	var report statusReport
	if code != 1 || json.Unmarshal([]byte(out), &report) != nil || report.OK || report.Latest.Panes != 2 {
		t.Fatalf("json: code=%d out=%q", code, out)
	}
}

func TestLogsUsesThePlatformViewer(t *testing.T) {
	var got []string
	previous := execInteractive
	execInteractive = func(_ *env, name string, args ...string) error {
		got = append([]string{name}, args...)
		return nil
	}
	t.Cleanup(func() { execInteractive = previous })
	if code, _, _ := runCLI(t, t.TempDir(), "logs", "-n", "5", "-f"); code != 0 || strings.Join(got, " ") != "journalctl --user -u herdr-recall -n 5 --no-pager -f" {
		t.Fatalf("code=%d got=%v", code, got)
	}
}

func TestConfigShowNeverPrintsTheToken(t *testing.T) {
	home := t.TempDir()
	paths := agent.PathsFor(home)
	if err := agent.Save(paths, "http://127.0.0.1:8788", "tok_"+strings.Repeat("a", 40), nil); err != nil {
		t.Skipf("Save recusou o token sintético: %v", err)
	}
	code, out, _ := runCLI(t, home, "config")
	if code != 0 || !strings.Contains(out, "http://127.0.0.1:8788") || !strings.Contains(out, "presente, 0600") || strings.Contains(out, strings.Repeat("a", 40)) {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if code, out, _ := runCLI(t, home, "config", "path"); code != 0 || out != filepath.Dir(paths.Config)+"\n" {
		t.Fatalf("path: %q", out)
	}
}
