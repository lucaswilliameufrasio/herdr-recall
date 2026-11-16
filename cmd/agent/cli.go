package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// env carries everything a command may touch, so tests can drive the CLI
// without the real home, terminal or network.
type env struct {
	in      io.Reader
	out     io.Writer
	errW    io.Writer
	home    string
	homeErr error
	client  *http.Client
	goos    string
	tty     bool
}

// exitError ends a command with a specific exit code. The command has already
// written its own message.
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("saída %d", int(e)) }

func exitWith(code int) error {
	if code == 0 {
		return nil
	}
	return exitError(code)
}

// usageError means the command line itself was wrong: exit 2.
type usageError struct {
	message string
}

func (e usageError) Error() string { return e.message }

func usagef(format string, args ...any) error {
	return usageError{message: fmt.Sprintf(format, args...)}
}

const (
	groupStart  = "start"
	groupDesk   = "desk"
	groupAgent  = "agent"
	groupUpkeep = "upkeep"
)

func newRoot(e *env) *cobra.Command {
	var showVersion bool
	root := &cobra.Command{
		Use:   "herdr-recall",
		Short: "Guarda a mesa do Herdr para você retomar depois de uma queda",
		Long: "herdr-recall guarda a mesa do Herdr (workspaces, abas, panes, harness e o comando\n" +
			"que retoma cada sessão) e envia ao servidor para você consultar depois de uma queda.",
		Example: "  herdr-recall               abre a mesa no terminal (TUI)\n" +
			"  herdr-recall setup         enrola esta máquina no servidor\n" +
			"  herdr-recall status        mostra se está tudo funcionando\n" +
			"  herdr-recall latest        imprime a última mesa capturada",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				printVersion(e.out, false)
				return nil
			}
			if !e.tty {
				return cmd.Help()
			}
			return browseCommand(e)
		},
	}
	root.SetIn(e.in)
	root.SetOut(e.out)
	root.SetErr(e.errW)
	root.Flags().BoolVarP(&showVersion, "version", "V", false, "mostra a versão")
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageError{message: flagMessage(err) + "\nVeja " + cmd.CommandPath() + " --help."}
	})
	root.AddGroup(
		&cobra.Group{ID: groupStart, Title: "Começo:"},
		&cobra.Group{ID: groupDesk, Title: "Mesa:"},
		&cobra.Group{ID: groupAgent, Title: "Agente e diagnóstico:"},
		&cobra.Group{ID: groupUpkeep, Title: "Manutenção:"},
	)
	root.SetHelpCommand(&cobra.Command{
		Use:     "help [comando]",
		Short:   "Mostra a ajuda de um comando",
		GroupID: groupUpkeep,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, _, err := root.Find(args)
			if target == nil || err != nil {
				return usagef("Comando desconhecido: %s\nVeja herdr-recall --help.", strings.Join(args, " "))
			}
			return target.Help()
		},
	})
	root.AddCommand(
		setupCmd(e), enrollCmd(e), statusCmd(e),
		latestCmd(e), historyCmd(e), restoreCmd(e), captureCmd(e),
		serviceCmd(e), runCmd(e), verifyCmd(e), queueCmd(e), logsCmd(e), configCmd(e),
		updateCmd(e), uninstallCmd(e), versionCmd(e), completionCmd(e),
	)
	applyTemplates(root)
	return root
}

func dispatch(args []string, in io.Reader, out, errW io.Writer, home string, homeErr error, client *http.Client, goos string) int {
	return dispatchEnv(&env{in: in, out: out, errW: errW, home: home, homeErr: homeErr, client: client, goos: goos}, args)
}

func dispatchEnv(e *env, args []string) int {
	if e.in == nil {
		e.in = strings.NewReader("")
	}
	root := newRoot(e)
	root.SetArgs(args)
	_, err := root.ExecuteC()
	return exitCodeFor(e.errW, err)
}

func exitCodeFor(errW io.Writer, err error) int {
	if err == nil {
		return 0
	}
	var code exitError
	if errors.As(err, &code) {
		return int(code)
	}
	var usage usageError
	if errors.As(err, &usage) {
		fmt.Fprintln(errW, usage.message)
		return 2
	}
	message := err.Error()
	if strings.HasPrefix(message, "unknown command ") {
		name := strings.TrimPrefix(message, "unknown command ")
		if i := strings.Index(name, " for "); i >= 0 {
			name = name[:i]
		}
		fmt.Fprintf(errW, "Comando desconhecido: %s\nVeja herdr-recall --help.\n", strings.Trim(name, `"`))
		return 2
	}
	fmt.Fprintln(errW, message)
	return 1
}

func flagMessage(err error) string {
	message := err.Error()
	replacements := []struct{ from, to string }{
		{"unknown flag: ", "Opção desconhecida: "},
		{"unknown shorthand flag: ", "Opção desconhecida: "},
		{"flag needs an argument: ", "A opção precisa de um valor: "},
		{"invalid argument ", "Valor inválido "},
	}
	for _, r := range replacements {
		if strings.HasPrefix(message, r.from) {
			return r.to + strings.TrimPrefix(message, r.from)
		}
	}
	return message
}

// exactArgs rejects positional arguments with the command's own usage line.
func noArgs(usage string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return usageError{message: "uso: " + usage}
		}
		return nil
	}
}

func needHome(e *env, message string) error {
	if e.homeErr != nil || e.home == "" {
		fmt.Fprintln(e.errW, message)
		return exitError(1)
	}
	return nil
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func versionCmd(e *env) *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:     "version",
		Short:   "Mostra a versão, o commit e a plataforma",
		GroupID: groupUpkeep,
		Args:    noArgs("herdr-recall version [--short]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			printVersion(e.out, !short)
			return nil
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "só a primeira linha")
	return cmd
}

func completionCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "completion bash|zsh|fish",
		Short: "Gera o autocompletar do shell",
		Long: "Gera o script de autocompletar. Exemplos:\n\n" +
			"  herdr-recall completion fish > ~/.config/fish/completions/herdr-recall.fish\n" +
			"  herdr-recall completion zsh > \"${fpath[1]}/_herdr-recall\"\n" +
			"  herdr-recall completion bash > ~/.local/share/bash-completion/completions/herdr-recall",
		GroupID:   groupUpkeep,
		ValidArgs: []string{"bash", "zsh", "fish"},
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usagef("uso: herdr-recall completion bash|zsh|fish")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(e.out, true)
			case "zsh":
				return root.GenZshCompletion(e.out)
			case "fish":
				return root.GenFishCompletion(e.out, true)
			default:
				return usagef("uso: herdr-recall completion bash|zsh|fish")
			}
		},
	}
}

func applyTemplates(root *cobra.Command) {
	root.InitDefaultHelpFlag()
	if flag := root.Flags().Lookup("help"); flag != nil {
		flag.Usage = "mostra esta ajuda"
	}
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.InitDefaultHelpFlag()
		if flag := cmd.Flags().Lookup("help"); flag != nil {
			flag.Usage = "mostra esta ajuda"
		}
		cmd.SetUsageTemplate(usageTemplate)
		cmd.DisableFlagsInUseLine = true
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
}

const usageTemplate = `Uso:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [comando]{{end}}{{if gt (len .Aliases) 0}}

Apelidos:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

{{if .HasParent}}Exemplos:{{else}}Comece por aqui:{{end}}
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Comandos:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Outros:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Opções:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Opções globais:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [comando] --help" para ver os detalhes de um comando.{{end}}
`
