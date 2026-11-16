package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"herdr-recall/internal/agent"
)

// ANSI 16 colors follow the terminal theme, so they stay legible on light and
// dark backgrounds and over SSH. lipgloss.Fprint drops them when the writer is
// not a terminal or NO_COLOR is set.
var (
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleFail    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleMuted   = lipgloss.NewStyle().Faint(true)
	styleAccent  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleBold    = lipgloss.NewStyle().Bold(true)
	styleHeading = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
)

func writeLine(w io.Writer, parts ...string) {
	lipgloss.Fprintln(w, strings.Join(parts, ""))
}

func statusMark(status agent.Status) string {
	switch status {
	case agent.StatusOK:
		return styleOK.Render("✓")
	case agent.StatusWarn:
		return styleWarn.Render("!")
	default:
		return styleFail.Render("✗")
	}
}

func label(text string, width int) string {
	return styleMuted.Render(pad(text, width))
}

// ago describes how long ago t was, in Portuguese.
func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 0:
		return "agora"
	case d < time.Minute:
		return "há menos de 1 min"
	case d < time.Hour:
		return fmt.Sprintf("há %d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("há %d h", int(d.Hours()))
	default:
		return fmt.Sprintf("há %d dias", int(d.Hours()/24))
	}
}

func localStamp(t time.Time) string {
	return t.Local().Format("02/01 15:04")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
