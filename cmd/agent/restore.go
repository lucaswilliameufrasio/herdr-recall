package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"herdr-recall/internal/agent"
	"herdr-recall/internal/contract"
)

// deskSource picks the desk to restore: latest.json, or entry N of the history.
type deskSource struct {
	latest     string
	historyDir string
	from       int
}

var errHistoryIndex = errors.New("índice do histórico")

func (s deskSource) load() (contract.Snapshot, bool, error) {
	if s.from <= 0 {
		raw, err := os.ReadFile(s.latest)
		if os.IsNotExist(err) {
			return contract.Snapshot{}, false, nil
		}
		if err != nil {
			return contract.Snapshot{}, false, err
		}
		desk, err := contract.ValidateSnapshot(raw)
		return desk, err == nil, err
	}
	entries, err := agent.ReadHistory(filepath.Dir(s.historyDir))
	if err != nil {
		return contract.Snapshot{}, false, err
	}
	if s.from > len(entries) {
		return contract.Snapshot{}, false, errHistoryIndex
	}
	desk, err := entries[s.from-1].Snapshot()
	return desk, err == nil, err
}

func restoreCommand(session string, plan bool, out, errW io.Writer, source deskSource, run agent.Runner) error {
	if err := agent.RejectSession(session); err != nil {
		fmt.Fprintln(errW, restoreMessage(err))
		return err
	}
	desk, found, err := source.load()
	if errors.Is(err, errHistoryIndex) {
		fmt.Fprintf(errW, "O histórico não tem a captura %d. Veja herdr-recall history.\n", source.from)
		return errUsage
	}
	if err != nil {
		fmt.Fprintln(errW, "Não foi possível ler a captura.")
		return err
	}
	if !found || (len(desk.Panes) == 0 && len(desk.Layouts) == 0) {
		fmt.Fprintln(out, "Nenhum pane nesta captura.")
		fmt.Fprintln(errW, "restore panes=0")
		return nil
	}
	report, err := agent.Restore(context.Background(), session, desk, plan, run)
	if err != nil {
		fmt.Fprintln(errW, restoreMessage(err))
		return err
	}
	if report.Plan {
		for _, line := range report.Lines {
			fmt.Fprintln(out, line)
		}
		fmt.Fprintln(out, "Nada foi executado.")
		fmt.Fprintf(errW, "restore plan panes=%d\n", report.Panes)
		return nil
	}
	fmt.Fprintln(out, "Mesa montada.")
	fmt.Fprintf(errW, "restore panes=%d workspaces=%d\n", report.Panes, report.Workspaces)
	return nil
}

func restoreMessage(err error) string {
	switch {
	case errors.Is(err, agent.ErrDefaultSession):
		return "A sessão default fica como está."
	case errors.Is(err, agent.ErrSessionName):
		return "O nome da sessão é inválido."
	case errors.Is(err, agent.ErrNoLayout):
		return "Esta captura não tem a divisão da mesa."
	case errors.Is(err, agent.ErrSessionOccupied):
		return "A sessão já tem workspaces. Nada foi executado."
	case errors.Is(err, agent.ErrHerdr):
		return "O Herdr não respondeu nesta sessão. Abra ela antes, com herdr --session NOME."
	default:
		return "Não foi possível montar a mesa."
	}
}

func restoreExit(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, errUsage) || errors.Is(err, agent.ErrDefaultSession) || errors.Is(err, agent.ErrSessionName) {
		return 2
	}
	return 1
}
