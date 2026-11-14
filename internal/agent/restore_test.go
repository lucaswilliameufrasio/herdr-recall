package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"herdr-recall/internal/contract"
)

func TestRestoreBuildsTheEmptySessionAndSkipsLowConfidence(t *testing.T) {
	fake := &herdrFake{}
	report, err := Restore(context.Background(), "limpa", restoreDesk(), false, fake.run)
	if err != nil {
		t.Fatal(err)
	}
	if report.Plan || report.Workspaces != 1 || report.Panes != 3 {
		t.Fatalf("relato: %+v", report)
	}
	want := [][]string{
		{"herdr", "--session", "limpa", "api", "snapshot"},
		{"herdr", "--session", "limpa", "workspace", "create", "--label", "mesa", "--cwd", "/tmp/a", "--no-focus"},
		{"herdr", "--session", "limpa", "tab", "rename", "nt", "aba"},
		{"herdr", "--session", "limpa", "pane", "split", "--pane", "np", "--direction", "right", "--ratio", "0.5", "--cwd", "/home/project", "--no-focus"},
		{"herdr", "--session", "limpa", "tab", "create", "--workspace", "nw", "--label", "outra", "--cwd", "/tmp/solo", "--no-focus"},
		{"herdr", "--session", "limpa", "pane", "zoom", "--pane", "tp", "--on"},
		{"herdr", "--session", "limpa", "agent", "start", "opencode1", "--kind", "opencode", "--pane", "sp", "--timeout", "60000", "--", "--session", "ses_abcdef12"},
	}
	if len(fake.calls) != len(want) {
		t.Fatalf("comandos: %d\n%v", len(fake.calls), fake.calls)
	}
	for i := range want {
		if strings.Join(fake.calls[i], "\x00") != strings.Join(want[i], "\x00") {
			t.Fatalf("comando %d:\n got %v\nwant %v", i, fake.calls[i], want[i])
		}
	}
}

func TestRestorePlanDoesNotCreate(t *testing.T) {
	fake := &herdrFake{}
	report, err := Restore(context.Background(), "limpa", restoreDesk(), true, fake.run)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 || fake.calls[0][3] != "api" {
		t.Fatalf("plano chamou o Herdr: %v", fake.calls)
	}
	text := strings.Join(report.Lines, "\n")
	if !strings.Contains(text, "workspace 'mesa'") || !strings.Contains(text, "harness 'opencode' '--session' 'ses_abcdef12'") || !strings.Contains(text, "zoom") {
		t.Fatalf("plano: %s", text)
	}
}

func TestRestoreRefusesTheDefaultSessionWithoutCallingHerdr(t *testing.T) {
	fake := &herdrFake{}
	_, err := Restore(context.Background(), "default", restoreDesk(), false, fake.run)
	if !errors.Is(err, ErrDefaultSession) || len(fake.calls) != 0 {
		t.Fatalf("default: %v calls=%d", err, len(fake.calls))
	}
	_, err = Restore(context.Background(), "limpa", contract.Snapshot{Panes: restoreDesk().Panes}, false, fake.run)
	if !errors.Is(err, ErrNoLayout) || len(fake.calls) != 0 {
		t.Fatalf("sem divisão: %v calls=%d", err, len(fake.calls))
	}
}

func TestRestoreStopsWhenTheSessionAlreadyHasWorkspaces(t *testing.T) {
	fake := &herdrFake{occupied: true}
	_, err := Restore(context.Background(), "limpa", restoreDesk(), false, fake.run)
	if !errors.Is(err, ErrSessionOccupied) || len(fake.calls) != 1 {
		t.Fatalf("ocupada: %v calls=%d", err, len(fake.calls))
	}
}

func TestRestoreStopsAfterAFailedSplit(t *testing.T) {
	fake := &herdrFake{failSplit: true}
	_, err := Restore(context.Background(), "limpa", restoreDesk(), false, fake.run)
	if !errors.Is(err, ErrRestore) {
		t.Fatal(err)
	}
	for _, call := range fake.calls {
		if len(call) > 3 && call[3] == "agent" {
			t.Fatalf("harness depois da falha: %v", fake.calls)
		}
	}
}

func TestRestoreReportsHerdrDown(t *testing.T) {
	fake := &herdrFake{failAll: true}
	_, err := Restore(context.Background(), "limpa", restoreDesk(), true, fake.run)
	if !errors.Is(err, ErrHerdr) {
		t.Fatal(err)
	}
}

type herdrFake struct {
	calls     [][]string
	occupied  bool
	failSplit bool
	failAll   bool
}

func (f *herdrFake) run(ctx context.Context, argv []string) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	f.calls = append(f.calls, append([]string{}, argv...))
	if f.failAll {
		return nil, errors.New("down")
	}
	joined := strings.Join(argv, "\x00")
	switch {
	case strings.Contains(joined, "api\x00snapshot"):
		if f.occupied {
			return []byte(`{"result":{"snapshot":{"workspaces":[{"workspace_id":"w9"}]}}}`), nil
		}
		return []byte(`{"result":{"snapshot":{"workspaces":[]}}}`), nil
	case strings.Contains(joined, "workspace\x00create"):
		return []byte(`{"result":{"workspace":{"workspace_id":"nw","number":1},"tab":{"tab_id":"nt"},"root_pane":{"pane_id":"np"}}}`), nil
	case strings.Contains(joined, "tab\x00rename"):
		return []byte(`{"result":{"tab":{"tab_id":"nt"}}}`), nil
	case strings.Contains(joined, "pane\x00split"):
		if f.failSplit {
			return nil, errors.New("split")
		}
		return []byte(`{"result":{"pane":{"pane_id":"sp"}}}`), nil
	case strings.Contains(joined, "tab\x00create"):
		return []byte(`{"result":{"tab":{"tab_id":"nt2"},"root_pane":{"pane_id":"tp"}}}`), nil
	case strings.Contains(joined, "pane\x00zoom"):
		return []byte(`{"result":{"pane":{"pane_id":"tp"}}}`), nil
	case strings.Contains(joined, "agent\x00start"):
		return []byte(`{"result":{"agent":"opencode1"}}`), nil
	default:
		return nil, errors.New("comando inesperado")
	}
}

func restoreDesk() contract.Snapshot {
	return contract.Snapshot{
		SnapshotID: "0123456789abcdef",
		Host:       "desk",
		BootID:     "boot-1",
		CapturedAt: "2026-11-13T00:00:00.000000000Z",
		Panes: []contract.Pane{
			{
				WorkspaceID: "w1", WorkspaceLabel: "mesa", TabID: "t1", TabLabel: "aba",
				PaneID: "p0", Cwd: "/tmp/a", MatchBasis: contract.MatchNone, Confidence: contract.ConfidenceLow,
			},
			{
				WorkspaceID: "w1", WorkspaceLabel: "mesa", TabID: "t1", TabLabel: "aba",
				PaneID: "p1", Cwd: "/home/project",
				HerdrSession: "ses_abcdef12", Harness: contract.HarnessOpenCode, SessionID: "ses_abcdef12",
				ResumeArgv: []string{"opencode", "--session", "ses_abcdef12"},
				MatchBasis: contract.MatchHerdrAgentSession, Confidence: contract.ConfidenceHigh,
			},
			{
				WorkspaceID: "w1", WorkspaceLabel: "mesa", TabID: "t2", TabLabel: "outra",
				PaneID: "p2", Cwd: "/tmp/solo", MatchBasis: contract.MatchNone, Confidence: contract.ConfidenceLow,
			},
		},
		Layouts: []contract.Layout{
			{
				WorkspaceID: "w1", TabID: "t1", WorkspaceNumber: 1, TabNumber: 1,
				Root: &contract.Node{
					Type: contract.NodeSplit, Direction: contract.SplitRight, Ratio: 0.5,
					First:  &contract.Node{Type: contract.NodePane, PaneID: "p0"},
					Second: &contract.Node{Type: contract.NodePane, PaneID: "p1"},
				},
			},
			{
				WorkspaceID: "w1", TabID: "t2", WorkspaceNumber: 1, TabNumber: 2, Zoomed: true,
				Root: &contract.Node{Type: contract.NodePane, PaneID: "p2"},
			},
		},
	}
}
