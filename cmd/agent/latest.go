package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"herdr-recall/internal/contract"
)

func run(args []string, out, log io.Writer, latestPath string) error {
	if len(args) > 0 && args[0] == "latest" {
		return printLatest(out, log, latestPath)
	}
	fmt.Fprintln(out, "herdr-recall")
	return nil
}

func defaultLatestPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "herdr-recall", "latest.json"), nil
}

func printLatest(out, log io.Writer, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(out, "Nenhum pane nesta captura.")
			fmt.Fprintln(log, "latest panes=0")
			return nil
		}
		return err
	}
	snap, err := contract.ValidateSnapshot(raw)
	if err != nil {
		return err
	}
	if len(snap.Panes) == 0 {
		fmt.Fprintln(out, "Nenhum pane nesta captura.")
		fmt.Fprintln(log, "latest panes=0")
		return nil
	}
	header := []string{"workspace", "aba", "pane", "diretório", "harness", "sessão", "confiança", "comando"}
	rows := make([][]string, 0, len(snap.Panes))
	for _, pane := range snap.Panes {
		rows = append(rows, []string{
			cell(pane.WorkspaceLabel),
			cell(pane.TabLabel),
			cell(pane.PaneLabel),
			cell(pane.Cwd),
			cell(pane.Harness),
			cell(pane.SessionID),
			confidenceCell(pane.Confidence),
			commandLine(pane.ResumeArgv),
		})
	}
	widths := make([]int, len(header))
	for i, name := range header {
		widths[i] = utf8.RuneCountInString(name)
	}
	for _, row := range rows {
		for i, value := range row {
			if i == len(row)-1 {
				continue
			}
			width := utf8.RuneCountInString(value)
			if width > widths[i] {
				widths[i] = width
			}
		}
	}
	writeRow(out, header, widths)
	for _, row := range rows {
		writeRow(out, row, widths)
	}
	fmt.Fprintf(log, "latest panes=%d\n", len(snap.Panes))
	return nil
}

func writeRow(out io.Writer, row []string, widths []int) {
	for i, value := range row {
		if i > 0 {
			fmt.Fprint(out, "  ")
		}
		if i == len(row)-1 {
			fmt.Fprint(out, value)
			continue
		}
		fmt.Fprint(out, pad(value, widths[i]))
	}
	fmt.Fprintln(out)
}

func cell(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

func confidenceCell(value string) string {
	switch value {
	case contract.ConfidenceHigh:
		return "alta"
	case contract.ConfidenceLow:
		return "baixa"
	default:
		return "—"
	}
}

func commandLine(argv []string) string {
	if len(argv) == 0 {
		return "—"
	}
	parts := make([]string, len(argv))
	for i, arg := range argv {
		parts[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(parts, " ")
}

func pad(value string, width int) string {
	extra := width - utf8.RuneCountInString(value)
	if extra <= 0 {
		return value
	}
	return value + strings.Repeat(" ", extra)
}
