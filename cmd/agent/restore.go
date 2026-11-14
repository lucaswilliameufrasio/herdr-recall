package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"herdr-recall/internal/agent"
	"herdr-recall/internal/contract"
)

func restoreCommand(args []string, out, errW io.Writer, latestPath string, run agent.Runner) error {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	flags.SetOutput(errW)
	session := flags.String("session", "", "sessão nomeada vazia")
	plan := flags.Bool("plan", false, "mostra o plano sem criar pane")
	flags.Usage = func() {
		fmt.Fprintln(errW, "uso: herdr-recall restore --session NOME [--plan]")
	}
	if err := flags.Parse(args); err != nil {
		return errUsage
	}
	if *session == "" || len(flags.Args()) > 0 {
		flags.Usage()
		return errUsage
	}
	if err := agent.RejectSession(*session); err != nil {
		fmt.Fprintln(errW, restoreMessage(err))
		return err
	}
	raw, err := os.ReadFile(latestPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(out, "Nenhum pane nesta captura.")
			fmt.Fprintln(errW, "restore panes=0")
			return nil
		}
		return err
	}
	desk, err := contract.ValidateSnapshot(raw)
	if err != nil {
		return err
	}
	if len(desk.Panes) == 0 && len(desk.Layouts) == 0 {
		fmt.Fprintln(out, "Nenhum pane nesta captura.")
		fmt.Fprintln(errW, "restore panes=0")
		return nil
	}
	report, err := agent.Restore(context.Background(), *session, desk, *plan, run)
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
