package web

import (
	"strings"

	"herdr-recall/internal/contract"
)

// DeskPane is one row prepared from a validated snapshot.
type DeskPane struct {
	Workspace  string
	Tab        string
	Pane       string
	Directory  string
	Harness    string
	Session    string
	Confidence string
	Command    string
}

// DeskPanes prepares the values displayed in a restore table.
func DeskPanes(snapshot contract.Snapshot) []DeskPane {
	rows := make([]DeskPane, 0, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		row := DeskPane{
			Workspace: pane.WorkspaceLabel,
			Tab:       pane.TabLabel,
			Pane:      pane.PaneLabel,
			Directory: pane.Cwd,
			Harness:   pane.Harness,
			Session:   pane.SessionID,
		}
		switch pane.Confidence {
		case contract.ConfidenceHigh:
			row.Confidence = "alta"
		case contract.ConfidenceLow:
			row.Confidence = "baixa"
		default:
			row.Confidence = "—"
		}
		if contract.AllowArgv(pane.Harness, pane.ResumeArgv) && pane.ResumeArgv[2] == pane.SessionID {
			parts := make([]string, 0, len(pane.ResumeArgv))
			for _, arg := range pane.ResumeArgv {
				parts = append(parts, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
			}
			row.Command = strings.Join(parts, " ")
		}
		rows = append(rows, row)
	}
	return rows
}
