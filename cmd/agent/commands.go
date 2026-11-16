package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"herdr-recall/internal/agent"
)

func goosName() string { return runtime.GOOS }

func stateDir(home string) string {
	return filepath.Join(home, ".local", "state", "herdr-recall")
}

func latestPath(home string) string {
	return filepath.Join(stateDir(home), "latest.json")
}

func setupCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:     "setup",
		Short:   "Enrola esta máquina no servidor, com perguntas",
		Long:    "Pergunta a URL do servidor, o código de uso único gerado no painel e o nome da máquina.\nGrava a configuração e o token privados em ~/.config/herdr-recall.",
		GroupID: groupStart,
		Args:    noArgs("herdr-recall setup"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := needHome(e, "Não foi possível enrolar esta máquina."); err != nil {
				return err
			}
			paths := agent.PathsFor(e.home)
			err := setup(e.in, e.out, e.errW, paths, e.client, unitInstalled(e.home, e.goos))
			return enrollResult(e, err)
		},
	}
}

func enrollCmd(e *env) *cobra.Command {
	var api, code, name string
	cmd := &cobra.Command{
		Use:     "enroll --api URL --code CODIGO --name NOME",
		Short:   "Enrola esta máquina sem perguntas (para scripts)",
		GroupID: groupStart,
		Args:    noArgs("herdr-recall enroll --api URL --code CODIGO --name NOME"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if api == "" || code == "" || name == "" {
				return usagef("uso: herdr-recall enroll --api URL --code CODIGO --name NOME")
			}
			if err := needHome(e, "Não foi possível enrolar esta máquina."); err != nil {
				return err
			}
			paths := agent.PathsFor(e.home)
			err := exchange(e.out, e.errW, paths, api, code, name, e.client, unitInstalled(e.home, e.goos))
			return enrollResult(e, err)
		},
	}
	cmd.Flags().StringVar(&api, "api", "", "`URL` do servidor")
	cmd.Flags().StringVar(&code, "code", "", "`CODIGO` de uso único gerado no painel")
	cmd.Flags().StringVar(&name, "name", "", "`NOME` da máquina")
	return cmd
}

func enrollResult(e *env, err error) error {
	if err == nil {
		return nil
	}
	if errorsIsUsage(err) {
		return exitError(2)
	}
	fmt.Fprintln(e.errW, enrollMessage(err))
	return exitError(1)
}

func errorsIsUsage(err error) bool {
	return err == errUsage
}

func latestCmd(e *env) *cobra.Command {
	var opts latestOptions
	cmd := &cobra.Command{
		Use:     "latest",
		Short:   "Imprime a última mesa capturada, sem falar com o servidor",
		Long:    "Imprime a última mesa gravada nesta máquina. Funciona com o servidor fora do ar.\nA última coluna é o comando que retoma a sessão; nada é executado.",
		Example: "  herdr-recall latest\n  herdr-recall latest --workspace api --harness claude\n  herdr-recall latest --json | jq '.panes[].resume_argv'",
		GroupID: groupDesk,
		Args:    noArgs("herdr-recall latest [--json] [--workspace TEXTO] [--harness NOME]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := needHome(e, "Não foi possível mostrar as capturas."); err != nil {
				return err
			}
			if err := printLatest(e.out, e.errW, latestPath(e.home), opts); err != nil {
				fmt.Fprintln(e.errW, "Não foi possível mostrar as capturas.")
				return exitError(1)
			}
			return nil
		},
	}
	addDeskFilters(cmd, &opts)
	return cmd
}

func addDeskFilters(cmd *cobra.Command, opts *latestOptions) {
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "imprime a captura em JSON")
	cmd.Flags().StringVarP(&opts.Workspace, "workspace", "w", "", "só panes cujo workspace contém `TEXTO`")
	cmd.Flags().StringVar(&opts.Harness, "harness", "", "só panes deste `HARNESS` (claude, opencode, ...)")
}

func restoreCmd(e *env) *cobra.Command {
	var session string
	var plan bool
	var from int
	cmd := &cobra.Command{
		Use:   "restore --session NOME [--plan] [--from N]",
		Short: "Monta a mesa capturada numa sessão nomeada vazia do Herdr",
		Long: "Monta a última mesa (ou uma do histórico, com --from) numa sessão nomeada do Herdr\n" +
			"que já está aberta e não tem workspace. A sessão default fica como está.\n" +
			"Com --plan só mostra o que seria feito.",
		Example: "  herdr --session volta        # em outro terminal\n" +
			"  herdr-recall restore --session volta --plan\n" +
			"  herdr-recall restore --session volta\n" +
			"  herdr-recall restore --session volta --from 3",
		GroupID: groupDesk,
		Args:    noArgs("herdr-recall restore --session NOME [--plan] [--from N]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if session == "" {
				return usagef("uso: herdr-recall restore --session NOME [--plan] [--from N]")
			}
			if err := needHome(e, "Não foi possível montar a mesa."); err != nil {
				return err
			}
			source := deskSource{latest: latestPath(e.home), historyDir: filepath.Join(stateDir(e.home), "history"), from: from}
			return exitWith(restoreExit(restoreCommand(session, plan, e.out, e.errW, source, herdrRun)))
		},
	}
	cmd.Flags().StringVarP(&session, "session", "s", "", "`NOME` da sessão nomeada vazia do Herdr")
	cmd.Flags().BoolVar(&plan, "plan", false, "mostra o plano sem criar pane")
	cmd.Flags().IntVar(&from, "from", 0, "usa a captura `N` do histórico (1 = a mais recente; veja history)")
	return cmd
}

func captureCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:     "capture",
		Short:   "Grava uma captura local agora e para (não envia)",
		GroupID: groupDesk,
		Args:    noArgs("herdr-recall capture"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := needHome(e, "Não foi possível gravar a captura."); err != nil {
				return err
			}
			opt, err := deskOptions(e.home)
			if err != nil {
				fmt.Fprintln(e.errW, "Não foi possível gravar a captura.")
				return exitError(1)
			}
			return exitWith(captureCommand(e.out, e.errW, opt))
		},
	}
}

func runCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:     "run",
		Short:   "Captura a cada 60 s e envia a fila (é o que o serviço executa)",
		GroupID: groupAgent,
		Args:    noArgs("herdr-recall run"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := needHome(e, "Não foi possível gravar a captura."); err != nil {
				return err
			}
			opt, err := deskOptions(e.home)
			if err != nil {
				fmt.Fprintln(e.errW, "Não foi possível gravar a captura.")
				return exitError(1)
			}
			return exitWith(runCommand(e.errW, opt, e.home))
		},
	}
}

func verifyCmd(e *env) *cobra.Command {
	var send bool
	cmd := &cobra.Command{
		Use:     "verify [--send]",
		Short:   "Diagnostica a máquina; com --send faz uma captura e envio reais",
		GroupID: groupAgent,
		Args:    noArgs("herdr-recall verify [--send]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := needHome(e, "Não foi possível verificar esta máquina."); err != nil {
				return err
			}
			return exitWith(verifyCommand(send, e.out, e.errW, e.home, e.goos))
		},
	}
	cmd.Flags().BoolVar(&send, "send", false, "grava uma captura real e envia também os itens anteriores da fila")
	return cmd
}

const serviceUsage = "uso: herdr-recall service install [--dry-run] | uninstall | doctor"

func serviceCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "service",
		Short:   "Instala, remove e diagnostica o serviço do agente",
		GroupID: groupAgent,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("Subcomando desconhecido: %s\n%s", args[0], serviceUsage)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return usagef(serviceUsage)
		},
	}
	manager := func() (agent.Manager, error) {
		if err := needHome(e, "Não foi possível operar o serviço."); err != nil {
			return agent.Manager{}, err
		}
		if e.goos == "windows" {
			fmt.Fprintln(e.errW, "O Windows não tem serviço neste corte.")
			return agent.Manager{}, exitError(1)
		}
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(e.errW, "Não foi possível operar o serviço.")
			return agent.Manager{}, exitError(1)
		}
		return agent.NewManager(e.goos, e.home, exe, os.Getenv("USER"), os.Getuid()), nil
	}
	var dry bool
	install := &cobra.Command{
		Use:   "install [--dry-run]",
		Short: "Grava e liga o serviço de usuário (systemd ou launchd)",
		Args:  noArgs("herdr-recall service install [--dry-run]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			return exitWith(serviceInstall(dry, e.out, e.errW, m))
		},
	}
	install.Flags().BoolVar(&dry, "dry-run", false, "mostra a unidade sem gravar")
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Desliga e remove o serviço de usuário",
		Args:  noArgs("herdr-recall service uninstall"),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			return exitWith(serviceUninstall(e.out, e.errW, m))
		},
	}
	doctor := &cobra.Command{
		Use:   "doctor",
		Short: "Confere serviço, configuração, servidor e fila",
		Args:  noArgs("herdr-recall service doctor"),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := manager()
			if err != nil {
				return err
			}
			return exitWith(serviceDoctor(e.out, e.errW, m))
		},
	}
	cmd.AddCommand(install, uninstall, doctor)
	return cmd
}
