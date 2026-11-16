package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"herdr-recall/internal/agent"
	"herdr-recall/internal/release"
)

// Seams for tests.
var (
	releaseClient  = func(e *env) release.Client { return release.Client{HTTP: e.client} }
	executablePath = func() (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(exe)
	}
	runQuiet = func(ctx context.Context, name string, args ...string) error {
		return exec.CommandContext(ctx, name, args...).Run()
	}
	targetPlatform = func() (string, string) { return runtime.GOOS, runtime.GOARCH }
)

type updateOptions struct {
	check          bool
	tag            string
	allowDowngrade bool
	force          bool
	restart        bool
}

func updateCmd(e *env) *cobra.Command {
	var opts updateOptions
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Atualiza os binários para a última release (ou --tag)",
		Long: "Baixa a release, confere o SHA-256 e troca herdr-recall e herdr-recall-server no\n" +
			"diretório deste binário, cada um com uma renomeação atômica.\n\n" +
			"  Versão mais nova: atualiza.\n" +
			"  Mesma versão: não faz nada.\n" +
			"  Versão mais antiga: recusa, a não ser com --allow-downgrade.\n" +
			"  Este binário sem versão (build local): recusa, a não ser com --force.\n\n" +
			"O serviço continua com o binário antigo até reiniciar; --restart reinicia.",
		Example: "  herdr-recall update --check\n  herdr-recall update --restart\n  herdr-recall update --tag v0.4.0 --allow-downgrade",
		GroupID: groupUpkeep,
		Args:    noArgs("herdr-recall update [--check] [--tag vX.Y.Z] [--allow-downgrade] [--force] [--restart]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(e, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.check, "check", false, "só informa se há versão nova")
	cmd.Flags().StringVar(&opts.tag, "tag", "", "instala esta `vX.Y.Z` em vez da última")
	cmd.Flags().BoolVar(&opts.allowDowngrade, "allow-downgrade", false, "aceita voltar para uma versão mais antiga")
	cmd.Flags().BoolVar(&opts.force, "force", false, "substitui um binário sem versão (build local)")
	cmd.Flags().BoolVar(&opts.restart, "restart", false, "reinicia o serviço depois de trocar os binários")
	return cmd
}

func runUpdate(e *env, opts updateOptions) error {
	exe, err := executablePath()
	if err != nil {
		fmt.Fprintln(e.errW, "Não foi possível localizar este binário.")
		return exitError(1)
	}
	if filepath.Base(exe) != "herdr-recall" || strings.Contains(exe, "go-build") {
		fmt.Fprintf(e.errW, "Este binário (%s) não é uma instalação do herdr-recall; nada foi alterado.\n", exe)
		return exitError(1)
	}
	binDir := filepath.Dir(exe)
	client := releaseClient(e)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var target release.Version
	if opts.tag != "" {
		v, ok := release.Parse(opts.tag)
		if !ok {
			return usagef("A tag precisa ser vX.Y.Z, por exemplo v0.4.0.")
		}
		target = v
	} else {
		v, err := client.Latest(ctx)
		if err != nil {
			fmt.Fprintln(e.errW, "Não foi possível consultar a última release. Tente de novo ou use --tag vX.Y.Z.")
			return exitError(1)
		}
		target = v
	}

	decision := release.Decide(version, target)
	switch decision {
	case release.Same:
		writeLine(e.out, styleOK.Render("Já está na "+target.String()+"."), styleMuted.Render(" Nada foi alterado."))
		return nil
	case release.Downgrade:
		if opts.check {
			fmt.Fprintf(e.out, "Instalada: %s. A release %s é mais antiga.\n", version, target)
			return nil
		}
		if !opts.allowDowngrade {
			fmt.Fprintf(e.errW, "A versão instalada (%s) é mais nova que %s.\nPara voltar mesmo assim, use --allow-downgrade. Nada foi alterado.\n", version, target)
			return exitError(1)
		}
	case release.Unknown:
		if opts.check {
			fmt.Fprintf(e.out, "Este binário não tem versão (%s). Última release: %s.\n", version, target)
			return nil
		}
		if !opts.force {
			fmt.Fprintf(e.errW, "Este binário não tem versão (%s, build local).\nPara trocá-lo pela %s, use --force. Nada foi alterado.\n", version, target)
			return exitError(1)
		}
	case release.Upgrade:
		if opts.check {
			writeLine(e.out, "Instalada: ", version, styleMuted.Render(" → "), styleAccent.Render("disponível: "+target.String()))
			writeLine(e.out, styleMuted.Render("Atualize com herdr-recall update."))
			return nil
		}
	}

	goos, goarch := targetPlatform()
	staged, err := os.MkdirTemp("", "herdr-recall-update-")
	if err != nil {
		fmt.Fprintln(e.errW, "Não foi possível preparar a atualização.")
		return exitError(1)
	}
	defer os.RemoveAll(staged)
	fmt.Fprintf(e.out, "Baixando %s para %s/%s…\n", target, goos, goarch)
	if err := client.Download(ctx, target, goos, goarch, staged); err != nil {
		switch {
		case errors.Is(err, release.ErrChecksum):
			fmt.Fprintln(e.errW, "Checksum não confere; nada foi alterado.")
		case errors.Is(err, release.ErrUnsupported):
			fmt.Fprintf(e.errW, "Não há pacote para %s/%s.\n", goos, goarch)
		case errors.Is(err, release.ErrNoAsset):
			fmt.Fprintf(e.errW, "A release %s não tem o pacote esperado; nada foi alterado.\n", target)
		default:
			fmt.Fprintf(e.errW, "Download falhou; nada foi alterado: %v\n", err)
		}
		return exitError(1)
	}
	if goos == runtime.GOOS && goarch == runtime.GOARCH {
		out, err := exec.CommandContext(ctx, filepath.Join(staged, "herdr-recall"), "--version").Output()
		if err != nil || strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]) != "herdr-recall "+target.String() {
			fmt.Fprintf(e.errW, "O pacote não informa a versão %s; nada foi alterado.\n", target)
			return exitError(1)
		}
	}
	replace := []string{"herdr-recall"}
	if _, err := os.Stat(filepath.Join(binDir, "herdr-recall-server")); err == nil {
		replace = append(replace, "herdr-recall-server")
	}
	if err := release.Replace(staged, binDir, replace); err != nil {
		fmt.Fprintf(e.errW, "Não foi possível trocar os binários em %s: %v\n", binDir, err)
		return exitError(1)
	}
	switch decision {
	case release.Downgrade:
		writeLine(e.out, styleWarn.Render(fmt.Sprintf("Voltou de %s para %s", version, target)), " em ", binDir, ".")
		writeLine(e.out, styleMuted.Render("O estado gravado pela versão mais nova pode não ser lido pela antiga; a fila e o histórico ficam onde estão."))
	case release.Unknown:
		writeLine(e.out, styleOK.Render("Instalada a "+target.String()), " em ", binDir, ".")
	default:
		writeLine(e.out, styleOK.Render(fmt.Sprintf("Atualizado de %s para %s", version, target)), " em ", binDir, ".")
	}
	if len(replace) == 1 {
		writeLine(e.out, styleMuted.Render("herdr-recall-server não estava neste diretório e não foi instalado."))
	}
	return afterUpdate(ctx, e, opts.restart)
}

func afterUpdate(ctx context.Context, e *env, restart bool) error {
	if !unitInstalled(e.home, e.goos) {
		return nil
	}
	var argv []string
	switch e.goos {
	case "darwin":
		argv = []string{"launchctl", "kickstart", "-k", "gui/" + strconv.Itoa(os.Getuid()) + "/" + agent.LaunchLabel}
	default:
		argv = []string{"systemctl", "--user", "restart", agent.UnitName}
	}
	if !restart {
		writeLine(e.out, "O serviço segue com o binário antigo até reiniciar: ", styleAccent.Render(strings.Join(argv, " ")))
		writeLine(e.out, styleMuted.Render("Ou rode herdr-recall update --restart da próxima vez."))
		return nil
	}
	if err := runQuiet(ctx, argv[0], argv[1:]...); err != nil {
		fmt.Fprintf(e.errW, "Os binários foram trocados, mas o serviço não reiniciou: %s\n", strings.Join(argv, " "))
		return exitError(1)
	}
	writeLine(e.out, styleOK.Render("Serviço reiniciado."), styleMuted.Render(" Confira com herdr-recall status."))
	return nil
}
