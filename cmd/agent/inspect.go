package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"herdr-recall/internal/agent"
	"herdr-recall/internal/contract"
)

func historyCmd(e *env) *cobra.Command {
	var limit int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "history [--limit N]",
		Short: "Lista as mesas distintas das últimas 48 h",
		Long: "Lista as mesas que mudaram nas últimas 48 h, da mais recente para a mais antiga.\n" +
			"Use o número da primeira coluna em history show N ou restore --from N.",
		Example: "  herdr-recall history\n  herdr-recall history show 2\n  herdr-recall restore --session volta --from 2 --plan",
		GroupID: groupDesk,
		Args:    noArgs("herdr-recall history [--limit N] | history show N"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := needHome(e, "Não foi possível ler o histórico."); err != nil {
				return err
			}
			entries, err := agent.ReadHistory(stateDir(e.home))
			if err != nil {
				fmt.Fprintln(e.errW, "Não foi possível ler o histórico.")
				return exitError(1)
			}
			if limit > 0 && len(entries) > limit {
				entries = entries[:limit]
			}
			if asJSON {
				return writeHistoryJSON(e.out, entries)
			}
			renderHistory(e.out, entries)
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "mostra no máximo `N` capturas (0 = todas)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "imprime a lista em JSON")
	var opts latestOptions
	show := &cobra.Command{
		Use:   "show N",
		Short: "Imprime a mesa N do histórico, como latest",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usagef("uso: herdr-recall history show N")
			}
			if n, err := strconv.Atoi(args[0]); err != nil || n < 1 {
				return usagef("N precisa ser um número a partir de 1. Veja herdr-recall history.")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := needHome(e, "Não foi possível ler o histórico."); err != nil {
				return err
			}
			n, _ := strconv.Atoi(args[0])
			entries, err := agent.ReadHistory(stateDir(e.home))
			if err != nil {
				fmt.Fprintln(e.errW, "Não foi possível ler o histórico.")
				return exitError(1)
			}
			if n > len(entries) {
				return usagef("O histórico não tem a captura %d. Veja herdr-recall history.", n)
			}
			desk, err := entries[n-1].Snapshot()
			if err != nil {
				fmt.Fprintln(e.errW, "Esta captura do histórico não é válida.")
				return exitError(1)
			}
			return printDesk(e.out, e.errW, desk, opts)
		},
	}
	addDeskFilters(show, &opts)
	cmd.AddCommand(show)
	return cmd
}

type historyJSON struct {
	Index      int    `json:"index"`
	SnapshotID string `json:"snapshot_id"`
	BootID     string `json:"boot_id"`
	CapturedAt string `json:"captured_at"`
	Panes      int    `json:"panes"`
	Workspaces int    `json:"workspaces"`
	Tabs       int    `json:"tabs"`
	Resumable  int    `json:"resumable"`
}

func writeHistoryJSON(out io.Writer, entries []agent.HistoryEntry) error {
	list := make([]historyJSON, 0, len(entries))
	for i, entry := range entries {
		list = append(list, historyJSON{
			Index: i + 1, SnapshotID: entry.SnapshotID, BootID: entry.BootID,
			CapturedAt: contract.FormatTime(entry.CapturedAt), Panes: entry.Panes,
			Workspaces: entry.Workspaces, Tabs: entry.Tabs, Resumable: entry.Resumable,
		})
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(list)
}

func renderHistory(out io.Writer, entries []agent.HistoryEntry) {
	if len(entries) == 0 {
		fmt.Fprintln(out, "Nenhuma captura no histórico.")
		return
	}
	now := nowFunc()
	header := []string{"#", "capturada", "quando", "panes", "workspaces", "retomáveis", "boot"}
	rows := make([][]string, 0, len(entries))
	previousBoot := ""
	for i, entry := range entries {
		boot := shortID(entry.BootID)
		if entry.BootID == previousBoot {
			boot = "″"
		}
		previousBoot = entry.BootID
		rows = append(rows, []string{
			strconv.Itoa(i + 1), localStamp(entry.CapturedAt), ago(now, entry.CapturedAt),
			strconv.Itoa(entry.Panes), strconv.Itoa(entry.Workspaces), strconv.Itoa(entry.Resumable), boot,
		})
	}
	writeTable(out, header, rows)
}

// writeTable pads every column; the header is dimmed on a terminal.
func writeTable(out io.Writer, header []string, rows [][]string) {
	widths := make([]int, len(header))
	for i, name := range header {
		widths[i] = len([]rune(name))
	}
	for _, row := range rows {
		for i, value := range row {
			if n := len([]rune(value)); n > widths[i] {
				widths[i] = n
			}
		}
	}
	line := func(row []string) string {
		text := ""
		for i, value := range row {
			if i > 0 {
				text += "  "
			}
			if i == len(row)-1 {
				text += value
			} else {
				text += pad(value, widths[i])
			}
		}
		return text
	}
	writeLine(out, styleMuted.Render(line(header)))
	for _, row := range rows {
		writeLine(out, line(row))
	}
}

func queueCmd(e *env) *cobra.Command {
	var asJSON, all bool
	cmd := &cobra.Command{
		Use:   "queue [--json]",
		Short: "Mostra a fila de envio e a quarentena (sem o conteúdo)",
		Long: "Mostra os itens que ainda não foram confirmados pelo servidor e os que ficaram em\n" +
			"quarentena por conflito. Só tipo, id, hora e tamanho: o conteúdo pode ter caminhos\n" +
			"e dados de sessão. É uma leitura pontual; a fila muda enquanto o serviço envia.\n" +
			"Sai com código 1 se houver quarentena.",
		GroupID: groupAgent,
		Args:    noArgs("herdr-recall queue [--json]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := needHome(e, "Não foi possível ler a fila."); err != nil {
				return err
			}
			queue, err := agent.ReadQueue(stateDir(e.home))
			if err != nil {
				fmt.Fprintln(e.errW, "Não foi possível ler a fila. Preserve os arquivos locais e investigue permissões e integridade.")
				return exitError(1)
			}
			if asJSON {
				encoder := json.NewEncoder(e.out)
				encoder.SetIndent("", "  ")
				if err := encoder.Encode(queueJSON(queue)); err != nil {
					return err
				}
			} else {
				renderQueue(e.out, queue, all)
			}
			if len(queue.Conflicts) > 0 {
				return exitError(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "imprime a fila em JSON")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "lista todos os itens, não só os 10 mais antigos")
	return cmd
}

const queueShown = 10

type queueItemJSON struct {
	Kind       string `json:"kind"`
	SnapshotID string `json:"snapshot_id,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
	Bytes      int    `json:"bytes"`
}

func queueJSON(q agent.QueueState) map[string][]queueItemJSON {
	convert := func(list []agent.QueueEntry) []queueItemJSON {
		out := make([]queueItemJSON, 0, len(list))
		for _, item := range list {
			out = append(out, queueItemJSON{Kind: item.Kind, SnapshotID: item.SnapshotID, ObservedAt: item.ObservedAt, Bytes: item.Bytes})
		}
		return out
	}
	return map[string][]queueItemJSON{"pending": convert(q.Pending), "conflicts": convert(q.Conflicts)}
}

func renderQueue(out io.Writer, q agent.QueueState, all bool) {
	section := func(title string, list []agent.QueueEntry) {
		writeLine(out, styleHeading.Render(title), styleMuted.Render(fmt.Sprintf(" (%d)", len(list))))
		if len(list) == 0 {
			writeLine(out, styleMuted.Render("  nada"))
			return
		}
		desks, size := 0, 0
		for _, item := range list {
			if item.Kind == "snapshot" {
				desks++
			}
			size += item.Bytes
		}
		summary := fmt.Sprintf("  %s · %s · %s", plural(desks, "mesa", "mesas"), plural(len(list)-desks, "presença", "presenças"), humanBytes(size))
		if at, ok := contract.ParseTime(list[0].ObservedAt); ok {
			summary += " · mais antigo " + localStamp(at)
		}
		writeLine(out, styleMuted.Render(summary))
		shown := list
		if !all && len(shown) > queueShown {
			shown = shown[:queueShown]
		}
		rows := make([][]string, 0, len(shown))
		for i, item := range shown {
			kind := "presença"
			if item.Kind == "snapshot" {
				kind = "mesa"
			}
			when := item.ObservedAt
			if at, ok := contract.ParseTime(item.ObservedAt); ok {
				when = localStamp(at)
			}
			id := cell(shortID(item.SnapshotID))
			rows = append(rows, []string{"  " + strconv.Itoa(i+1), kind, id, cell(when), humanBytes(item.Bytes)})
		}
		writeTable(out, []string{"  #", "tipo", "mesa", "hora", "tamanho"}, rows)
		if len(shown) < len(list) {
			writeLine(out, styleMuted.Render(fmt.Sprintf("  … e mais %d. Use --all para ver todos.", len(list)-len(shown))))
		}
	}
	section("Pendentes", q.Pending)
	writeLine(out)
	section("Quarentena", q.Conflicts)
	if len(q.Conflicts) > 0 {
		writeLine(out)
		writeLine(out, styleFail.Render("Há itens em quarentena."), " Preserve os arquivos; investigue a causa antes de qualquer reenvio ou remoção.")
	}
}

func humanBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	}
}

// execInteractive runs a viewer (journalctl, tail) attached to the terminal.
var execInteractive = func(e *env, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = e.in
	cmd.Stdout = e.out
	cmd.Stderr = e.errW
	return cmd.Run()
}

func logsCmd(e *env) *cobra.Command {
	var lines int
	var follow bool
	cmd := &cobra.Command{
		Use:     "logs [-f] [-n N]",
		Short:   "Mostra o log do serviço (journalctl ou o arquivo do launchd)",
		Long:    "Mostra o log do serviço do agente. O log não leva argv, sessão nem cwd.",
		GroupID: groupAgent,
		Args:    noArgs("herdr-recall logs [-f] [-n N]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := needHome(e, "Não foi possível ler o log."); err != nil {
				return err
			}
			var name string
			var argv []string
			switch e.goos {
			case "linux":
				name = "journalctl"
				argv = []string{"--user", "-u", "herdr-recall", "-n", strconv.Itoa(lines), "--no-pager"}
				if follow {
					argv = append(argv, "-f")
				}
			case "darwin":
				path := filepath.Join(e.home, "Library", "Logs", "herdr-recall.log")
				if _, err := os.Stat(path); err != nil {
					fmt.Fprintln(e.errW, "Ainda não há log em "+path+".")
					return exitError(1)
				}
				name = "tail"
				argv = []string{"-n", strconv.Itoa(lines)}
				if follow {
					argv = append(argv, "-F")
				}
				argv = append(argv, path)
			default:
				fmt.Fprintln(e.errW, "Sem serviço nesta plataforma, portanto sem log.")
				return exitError(1)
			}
			if err := execInteractive(e, name, argv...); err != nil {
				fmt.Fprintf(e.errW, "%s falhou: %v\n", name, err)
				return exitError(1)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "últimas `N` linhas")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "acompanha o log até Ctrl+C")
	return cmd
}

func configCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config",
		Short:   "Mostra onde ficam configuração, token, estado e serviço",
		GroupID: groupAgent,
		Args:    noArgs("herdr-recall config [show | path]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return showConfig(e)
		},
	}
	show := &cobra.Command{
		Use:   "show",
		Short: "Mostra a configuração sem o token",
		Args:  noArgs("herdr-recall config show"),
		RunE:  func(cmd *cobra.Command, args []string) error { return showConfig(e) },
	}
	path := &cobra.Command{
		Use:   "path",
		Short: "Imprime só o diretório de configuração (para scripts)",
		Args:  noArgs("herdr-recall config path"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := needHome(e, "Não foi possível localizar a configuração."); err != nil {
				return err
			}
			fmt.Fprintln(e.out, filepath.Dir(agent.PathsFor(e.home).Config))
			return nil
		},
	}
	cmd.AddCommand(show, path)
	return cmd
}

func unitPath(home, goos string) string {
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", "dev.herdr-recall.agent.plist")
	case "windows":
		return ""
	default:
		return filepath.Join(home, ".config", "systemd", "user", "herdr-recall.service")
	}
}

func showConfig(e *env) error {
	if err := needHome(e, "Não foi possível localizar a configuração."); err != nil {
		return err
	}
	paths := agent.PathsFor(e.home)
	const width = 14
	row := func(name, value string) { writeLine(e.out, label(name, width), value) }
	config, err := agent.Load(paths.Config)
	switch {
	case err == nil:
		row("servidor", config.API)
	case os.IsNotExist(err):
		row("servidor", styleWarn.Render("não enrolada")+styleMuted.Render(" → herdr-recall setup"))
	default:
		row("servidor", styleFail.Render("configuração ilegível"))
	}
	row("configuração", paths.Config)
	tokenFile := paths.Token
	if config.TokenFile != "" {
		tokenFile = config.TokenFile
	}
	if info, err := os.Stat(tokenFile); err == nil && !info.IsDir() {
		mode := info.Mode().Perm()
		note := styleOK.Render(fmt.Sprintf("presente, %04o", mode))
		if mode&0o077 != 0 {
			note = styleWarn.Render(fmt.Sprintf("presente, %04o (deveria ser 0600)", mode))
		}
		row("token", tokenFile+styleMuted.Render(" · ")+note)
	} else {
		row("token", tokenFile+styleMuted.Render(" · ausente"))
	}
	row("estado", stateDir(e.home))
	if unit := unitPath(e.home, e.goos); unit != "" {
		state := styleMuted.Render(" · não instalada")
		if unitInstalled(e.home, e.goos) {
			state = styleMuted.Render(" · ") + styleOK.Render("instalada")
		}
		row("serviço", unit+state)
	}
	if exe, err := os.Executable(); err == nil {
		row("binário", exe)
	}
	return nil
}
