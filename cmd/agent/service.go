package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"herdr-recall/internal/agent"
)

func serviceCommand(args []string, out, errW io.Writer, manager agent.Manager) int {
	if len(args) == 0 {
		fmt.Fprintln(errW, "uso: herdr-recall service install [--dry-run] | uninstall | doctor")
		return 2
	}
	switch args[0] {
	case "install":
		return serviceInstall(args[1:], out, errW, manager)
	case "uninstall":
		return serviceUninstall(args[1:], out, errW, manager)
	case "doctor":
		return serviceDoctor(args[1:], out, errW, manager)
	default:
		fmt.Fprintln(errW, "uso: herdr-recall service install [--dry-run] | uninstall | doctor")
		return 2
	}
}

func serviceInstall(args []string, out, errW io.Writer, manager agent.Manager) int {
	flags := flag.NewFlagSet("service install", flag.ContinueOnError)
	flags.SetOutput(errW)
	dry := flags.Bool("dry-run", false, "mostra a unidade sem gravar")
	flags.Usage = func() {
		fmt.Fprintln(errW, "uso: herdr-recall service install [--dry-run]")
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) > 0 {
		flags.Usage()
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report, err := manager.Install(ctx, agent.InstallOptions{DryRun: *dry})
	if err != nil {
		fmt.Fprintln(errW, serviceMessage(err))
		return 1
	}
	if *dry {
		fmt.Fprint(out, report.Content)
		for _, command := range report.Commands {
			fmt.Fprintln(out, command)
		}
		fmt.Fprintln(out, "Nada foi instalado.")
		return 0
	}
	fmt.Fprintln(out, "Serviço instalado.")
	for _, note := range report.Notes {
		fmt.Fprintln(out, note)
	}
	return 0
}

func serviceUninstall(args []string, out, errW io.Writer, manager agent.Manager) int {
	if len(args) > 0 {
		fmt.Fprintln(errW, "uso: herdr-recall service uninstall")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report, err := manager.Uninstall(ctx)
	if err != nil {
		fmt.Fprintln(errW, serviceMessage(err))
		return 1
	}
	if !report.Changed {
		fmt.Fprintln(out, "O serviço não estava instalado.")
		return 0
	}
	fmt.Fprintln(out, "Serviço removido.")
	for _, note := range report.Notes {
		fmt.Fprintln(out, note)
	}
	return 0
}

func serviceDoctor(args []string, out, errW io.Writer, manager agent.Manager) int {
	if len(args) > 0 {
		fmt.Fprintln(errW, "uso: herdr-recall service doctor")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	checks := manager.Doctor(ctx)
	for _, check := range checks {
		if check.Fix != "" {
			fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", check.Status, check.Name, check.Detail, check.Fix)
		} else {
			fmt.Fprintf(out, "%s\t%s\t%s\n", check.Status, check.Name, check.Detail)
		}
	}
	if agent.Failed(checks) {
		fmt.Fprintln(errW, "doctor fail")
		return 1
	}
	fmt.Fprintln(errW, "doctor ok")
	return 0
}

func serviceMessage(err error) string {
	switch {
	case errors.Is(err, agent.ErrUnsupported):
		return "O Windows não tem serviço neste corte."
	case errors.Is(err, agent.ErrTemporaryBinary):
		return "Este binário é temporário. Instale o herdr-recall e rode o comando de lá."
	case errors.Is(err, agent.ErrAgentName):
		return "O executável não pode se chamar agent."
	case errors.Is(err, agent.ErrBinaryPath):
		return "O caminho do binário não serve."
	default:
		return err.Error()
	}
}
