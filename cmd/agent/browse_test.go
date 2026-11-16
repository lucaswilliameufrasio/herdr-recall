package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"herdr-recall/internal/contract"
)

func browserFixture(t *testing.T, run func(context.Context, []string) ([]byte, error)) browser {
	t.Helper()
	home := t.TempDir()
	latest := deskFixture(t, "1111111111111111", "2026-11-13T00:00:00.000000000Z", "api", "web", "docs")
	var desk contract.Snapshot
	desk, _ = contract.ValidateSnapshot(latest)
	for i, pane := range desk.Panes {
		desk.Layouts = append(desk.Layouts, contract.Layout{WorkspaceID: pane.WorkspaceID, TabID: pane.TabID, WorkspaceNumber: i + 1, TabNumber: 1, Root: &contract.Node{Type: contract.NodePane, PaneID: pane.PaneID}})
	}
	withLayout, err := contract.MarshalSnapshot(desk)
	if err != nil {
		t.Fatal(err)
	}
	older := deskFixture(t, "2222222222222222", "2026-11-12T22:00:00.000000000Z", "web")
	writeState(t, home, map[string]string{
		"latest.json":            string(withLayout),
		"history/20261112.jsonl": string(older) + "\n",
		"history/20261113.jsonl": string(withLayout) + "\n",
	})
	m := newBrowser(loadBrowserData(&env{home: home, goos: "linux"}), run)
	m.now = func() time.Time { return time.Date(2026, 11, 13, 0, 10, 0, 0, time.UTC) }
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return next.(browser)
}

func press(t *testing.T, m browser, keys ...string) (browser, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		default:
			for _, r := range k {
				next, c := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
				m, cmd = next.(browser), c
			}
			continue
		}
		next, c := m.Update(msg)
		m, cmd = next.(browser), c
	}
	return m, cmd
}

func screen(m browser) string { return ansi.Strip(m.render()) }

func TestBrowserListsCapturesWithoutDuplicatingLatest(t *testing.T) {
	m := browserFixture(t, nil)
	if len(m.data.captures) != 2 || m.data.captures[0].title != "atual" || m.data.captures[1].history != 2 {
		t.Fatalf("capturas: %+v", m.data.captures)
	}
	out := screen(m)
	for _, want := range []string{"Capturas", "atual", "#2", "3 panes", "1 retomável", "não enrolada", "sem serviço"} {
		if !strings.Contains(out, want) {
			t.Fatalf("falta %q:\n%s", want, out)
		}
	}
	m, _ = press(t, m, "j")
	if out := screen(m); !strings.Contains(out, "1 pane ·") {
		t.Fatalf("segunda captura:\n%s", out)
	}
}

func TestBrowserFilterAndCopy(t *testing.T) {
	m := browserFixture(t, nil)
	m, _ = press(t, m, "/", "a", "p", "i", "enter")
	if panes := m.visiblePanes(); len(panes) != 1 || panes[0].WorkspaceLabel != "api" {
		t.Fatalf("filtro: %+v", panes)
	}
	if !strings.Contains(screen(m), "1/3") {
		t.Fatalf("contagem do filtro:\n%s", screen(m))
	}
	m, cmd := press(t, m, "c")
	if cmd == nil || m.flash != "Comando copiado. Nada foi executado." {
		t.Fatalf("cópia: %q", m.flash)
	}
	if !strings.Contains(screen(m), "'opencode' '--session' 'ses_abcdef12'") {
		t.Fatalf("comando no detalhe:\n%s", screen(m))
	}
	m, _ = press(t, m, "esc")
	if len(m.visiblePanes()) != 3 {
		t.Fatal("esc não limpou o filtro")
	}
	m, _ = press(t, m, "/", "w", "e", "b", "enter", "c")
	if m.flash != "Sem sessão para restaurar neste pane." {
		t.Fatalf("pane sem comando: %q", m.flash)
	}
}

func TestBrowserRestoreShowsPlanBeforeRunning(t *testing.T) {
	var calls [][]string
	run := func(_ context.Context, argv []string) ([]byte, error) {
		calls = append(calls, argv)
		if len(calls) == 1 {
			return []byte(`{"result":{"snapshot":{"workspaces":[]}}}`), nil
		}
		return nil, errors.New("herdr fora")
	}
	m := browserFixture(t, run)
	m, _ = press(t, m, "r", "d", "e", "f", "a", "u", "l", "t")
	m, cmd := press(t, m, "enter")
	if m.mode != modeSession || cmd == nil || !strings.Contains(m.flash, "default") {
		t.Fatalf("default aceito: mode=%v flash=%q", m.mode, m.flash)
	}
	m.session.SetValue("volta")
	m, cmd = press(t, m, "enter")
	if m.mode != modeRestoring || cmd == nil {
		t.Fatalf("sem plano: %v", m.mode)
	}
	next, _ := m.Update(cmd())
	m = next.(browser)
	if m.mode != modePlan || len(m.plan) == 0 || len(calls) != 1 {
		t.Fatalf("plano: mode=%v plan=%v calls=%d", m.mode, m.plan, len(calls))
	}
	if !strings.Contains(screen(m), "Plano para a sessão volta") {
		t.Fatalf("tela do plano:\n%s", screen(m))
	}
	m, cmd = press(t, m, "y")
	next, _ = m.Update(cmd())
	m = next.(browser)
	if m.mode != modeBrowse || m.flashOK || len(calls) < 2 {
		t.Fatalf("falha do herdr: mode=%v flash=%q calls=%d", m.mode, m.flash, len(calls))
	}
}

func TestBrowserWithoutCaptures(t *testing.T) {
	m := newBrowser(loadBrowserData(&env{home: t.TempDir(), goos: "linux"}), nil)
	if out := screen(m); !strings.Contains(out, "Nenhuma captura nesta máquina ainda.") {
		t.Fatalf("%s", out)
	}
	m, cmd := press(t, m, "q")
	if cmd == nil {
		t.Fatal("q não saiu")
	}
	_ = m
}

func TestMiddleTruncateKeepsBothEnds(t *testing.T) {
	if got := middleTruncate("/home/user/projects/very/long/path/to/repo", 20); ansi.StringWidth(got) != 20 || !strings.HasPrefix(got, "/home") || !strings.HasSuffix(got, "repo") {
		t.Fatalf("%q", got)
	}
}
