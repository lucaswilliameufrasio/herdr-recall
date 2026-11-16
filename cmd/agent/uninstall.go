package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"herdr-recall/internal/agent"
	"herdr-recall/internal/release"
)

// uninstallService is a seam so tests do not call systemctl.
var uninstallService = func(ctx context.Context, e *env, exe string) (agent.Report, error) {
	return agent.NewManager(e.goos, e.home, exe, os.Getenv("USER"), os.Getuid()).Uninstall(ctx)
}

type uninstallOptions struct {
	dryRun bool
	yes    bool
	purge  bool
}

func uninstallCmd(e *env) *cobra.Command {
	var opts uninstallOptions
	cmd := &cobra.Command{
		Use:   "uninstall [--dry-run] [--yes] [--purge]",
		Short: "Remove o serviço e os binários desta máquina",
		Long: "Remove o serviço do agente e os binários herdr-recall e herdr-recall-server do\n" +
			"diretório deste binário. Configuração, token, fila e histórico ficam, a não ser\n" +
			"com --purge. O banco de um herdr-recall-server nesta máquina nunca é apagado.\n" +
			"Fora de um terminal, exige --yes.",
		Example: "  herdr-recall uninstall --dry-run\n  herdr-recall uninstall\n  herdr-recall uninstall --purge --yes",
		GroupID: groupUpkeep,
		Args:    noArgs("herdr-recall uninstall [--dry-run] [--yes] [--purge]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := needHome(e, "Não foi possível localizar a instalação."); err != nil {
				return err
			}
			return runUninstall(e, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "só mostra o que seria removido")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "não pergunta (necessário fora de um terminal)")
	cmd.Flags().BoolVar(&opts.purge, "purge", false, "apaga também configuração, token, fila e histórico")
	return cmd
}

func runUninstall(e *env, opts uninstallOptions) error {
	exe, err := executablePath()
	if err != nil {
		fmt.Fprintln(e.errW, "Não foi possível localizar este binário.")
		return exitError(1)
	}
	if filepath.Base(exe) != "herdr-recall" || strings.Contains(exe, "go-build") {
		fmt.Fprintf(e.errW, "Este binário (%s) não é uma instalação do herdr-recall; nada foi removido.\n", exe)
		return exitError(1)
	}
	binDir := filepath.Dir(exe)
	var binaries []string
	for _, name := range release.Binaries {
		path := filepath.Join(binDir, name)
		if info, err := os.Lstat(path); err == nil && !info.IsDir() {
			binaries = append(binaries, path)
		}
	}
	unit := unitInstalled(e.home, e.goos)
	configDir := filepath.Dir(agent.PathsFor(e.home).Config)
	var purge []string
	if opts.purge {
		for _, dir := range []string{configDir, stateDir(e.home)} {
			if _, err := os.Stat(dir); err == nil {
				purge = append(purge, dir)
			}
		}
	}

	writeLine(e.out, styleBold.Render("Será removido:"))
	if unit {
		writeLine(e.out, "  serviço  ", unitPath(e.home, e.goos))
	}
	for _, path := range binaries {
		writeLine(e.out, "  binário  ", path)
	}
	for _, dir := range purge {
		writeLine(e.out, "  dados    ", dir)
	}
	if !unit && len(binaries) == 0 && len(purge) == 0 {
		fmt.Fprintln(e.out, "  nada")
		return nil
	}
	if opts.purge {
		if queue, err := agent.ReadQueue(stateDir(e.home)); err == nil && len(queue.Pending)+len(queue.Conflicts) > 0 {
			writeLine(e.out, styleWarn.Render(fmt.Sprintf("Atenção: %s na fila e %d em quarentena ainda não chegaram ao servidor e serão perdidos.", plural(len(queue.Pending), "item", "itens"), len(queue.Conflicts))))
		}
	} else {
		writeLine(e.out, styleMuted.Render("Ficam: "+configDir+" e "+stateDir(e.home)+" (use --purge para apagar)."))
	}
	if opts.dryRun {
		fmt.Fprintln(e.out, "Nada foi removido.")
		return nil
	}
	if !opts.yes {
		if !e.tty {
			fmt.Fprintln(e.errW, "Fora de um terminal, confirme com --yes. Nada foi removido.")
			return exitError(2)
		}
		fmt.Fprint(e.out, "Digite remover para confirmar: ")
		answer, _ := bufio.NewReader(e.in).ReadString('\n')
		if strings.TrimSpace(answer) != "remover" {
			fmt.Fprintln(e.out, "Cancelado. Nada foi removido.")
			return exitError(1)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if unit {
		if _, err := uninstallService(ctx, e, exe); err != nil {
			fmt.Fprintf(e.errW, "O serviço não foi removido (%s); os binários ficaram.\n", serviceMessage(err))
			return exitError(1)
		}
		writeLine(e.out, statusMark(agent.StatusOK), " serviço removido")
	}
	failed := false
	for _, path := range binaries {
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(e.errW, "Não foi possível remover %s: %v\n", path, err)
			failed = true
			continue
		}
		writeLine(e.out, statusMark(agent.StatusOK), " ", path)
	}
	for _, dir := range purge {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(e.errW, "Não foi possível apagar %s: %v\n", dir, err)
			failed = true
			continue
		}
		writeLine(e.out, statusMark(agent.StatusOK), " ", dir)
	}
	if failed {
		return exitError(1)
	}
	writeLine(e.out, styleOK.Render("herdr-recall removido."))
	return nil
}
