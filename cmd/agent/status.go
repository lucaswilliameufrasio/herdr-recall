package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"herdr-recall/internal/agent"
	"herdr-recall/internal/contract"
)

// doctorFunc lets tests replace the service and network checks.
var doctorFunc = func(ctx context.Context, e *env) []agent.Check {
	exe, err := os.Executable()
	if err != nil {
		return []agent.Check{{Name: "binário", Status: agent.StatusFail, Detail: "não foi possível localizar este binário"}}
	}
	return agent.NewManager(e.goos, e.home, exe, os.Getenv("USER"), os.Getuid()).Doctor(ctx)
}

var nowFunc = time.Now

type statusReport struct {
	Version    string        `json:"version"`
	Host       string        `json:"host,omitempty"`
	Enrolled   bool          `json:"enrolled"`
	API        string        `json:"api,omitempty"`
	Latest     *latestReport `json:"latest,omitempty"`
	History    int           `json:"history"`
	Pending    int           `json:"pending"`
	Conflicts  int           `json:"conflicts"`
	QueueError bool          `json:"queue_error,omitempty"`
	Checks     []checkReport `json:"checks"`
	OK         bool          `json:"ok"`
}

type latestReport struct {
	CapturedAt string `json:"captured_at"`
	Panes      int    `json:"panes"`
	Workspaces int    `json:"workspaces"`
	Resumable  int    `json:"resumable"`
}

type checkReport struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func statusCmd(e *env) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status [--json]",
		Short: "Mostra num relance se a máquina está guardando a mesa",
		Long: "Resume a última captura, a fila, o enrolamento e os checks do service doctor.\n" +
			"Sai com código 1 quando algum check falha.",
		GroupID: groupStart,
		Args:    noArgs("herdr-recall status [--json]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := needHome(e, "Não foi possível ler o estado desta máquina."); err != nil {
				return err
			}
			report := collectStatus(e)
			if asJSON {
				encoder := json.NewEncoder(e.out)
				encoder.SetIndent("", "  ")
				if err := encoder.Encode(report); err != nil {
					return err
				}
			} else {
				renderStatus(e, report)
			}
			if !report.OK {
				return exitError(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "imprime o resumo em JSON")
	return cmd
}

func collectStatus(e *env) statusReport {
	report := statusReport{Version: version, OK: true}
	report.Host, _ = agent.CurrentHost()
	if config, err := agent.Load(agent.PathsFor(e.home).Config); err == nil {
		report.API = config.API
		report.Enrolled = tokenExists(config.TokenFile)
	}
	if raw, err := os.ReadFile(latestPath(e.home)); err == nil {
		if snap, err := contract.ValidateSnapshot(raw); err == nil {
			latest := &latestReport{CapturedAt: snap.CapturedAt, Panes: len(snap.Panes)}
			workspaces := map[string]bool{}
			for _, pane := range snap.Panes {
				workspaces[pane.WorkspaceID] = true
				if len(pane.ResumeArgv) > 0 {
					latest.Resumable++
				}
			}
			latest.Workspaces = len(workspaces)
			report.Latest = latest
		}
	}
	if entries, err := agent.ReadHistory(stateDir(e.home)); err == nil {
		report.History = len(entries)
	}
	if queue, err := agent.ReadQueue(stateDir(e.home)); err == nil {
		report.Pending = len(queue.Pending)
		report.Conflicts = len(queue.Conflicts)
	} else {
		report.QueueError = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, check := range doctorFunc(ctx, e) {
		if check.Name == "fila" || check.Name == "conflitos" {
			// Shown in the summary above; still counted for the exit code.
			if check.Status == agent.StatusFail {
				report.OK = false
			}
			continue
		}
		report.Checks = append(report.Checks, checkReport{Name: check.Name, Status: string(check.Status), Detail: check.Detail, Fix: check.Fix})
		if check.Status == agent.StatusFail {
			report.OK = false
		}
	}
	if report.Conflicts > 0 || report.QueueError {
		report.OK = false
	}
	return report
}

func renderStatus(e *env, r statusReport) {
	w := e.out
	now := nowFunc()
	header := styleBold.Render("herdr-recall "+r.Version) + styleMuted.Render(" · "+e.goos)
	if r.Host != "" {
		header += styleMuted.Render(" · " + r.Host)
	}
	writeLine(w, header)
	writeLine(w)
	const width = 12
	if r.Enrolled {
		writeLine(w, "  ", label("Máquina", width), styleOK.Render("enrolada"), styleMuted.Render(" em "), r.API)
	} else {
		writeLine(w, "  ", label("Máquina", width), styleWarn.Render("não enrolada"), styleMuted.Render(" → herdr-recall setup"))
	}
	if r.Latest == nil {
		writeLine(w, "  ", label("Última mesa", width), styleWarn.Render("nenhuma captura"), styleMuted.Render(" → herdr-recall capture"))
	} else {
		when := r.Latest.CapturedAt
		if at, ok := contract.ParseTime(r.Latest.CapturedAt); ok {
			when = ago(now, at) + styleMuted.Render(" ("+localStamp(at)+")")
		}
		writeLine(w, "  ", label("Última mesa", width), when)
		writeLine(w, "  ", label("", width),
			plural(r.Latest.Panes, "pane", "panes"), styleMuted.Render(" em "),
			plural(r.Latest.Workspaces, "workspace", "workspaces"), styleMuted.Render(" · "),
			plural(r.Latest.Resumable, "retomável", "retomáveis"))
	}
	writeLine(w, "  ", label("Histórico", width), plural(r.History, "captura", "capturas"), styleMuted.Render(" nas últimas 48 h"))
	switch {
	case r.QueueError:
		writeLine(w, "  ", label("Fila", width), styleFail.Render("ilegível"), styleMuted.Render(" → preserve os arquivos e rode herdr-recall queue"))
	case r.Conflicts > 0:
		writeLine(w, "  ", label("Fila", width), fmt.Sprintf("%d pendentes · ", r.Pending), styleFail.Render(fmt.Sprintf("%d em quarentena", r.Conflicts)), styleMuted.Render(" → herdr-recall queue"))
	case r.Pending > 0:
		writeLine(w, "  ", label("Fila", width), styleWarn.Render(plural(r.Pending, "pendente", "pendentes")), styleMuted.Render(" · aguardando envio"))
	default:
		writeLine(w, "  ", label("Fila", width), styleOK.Render("vazia"))
	}
	writeLine(w)
	for _, check := range r.Checks {
		line := "  " + statusMark(agent.Status(check.Status)) + " " + pad(check.Name, 14) + styleMuted.Render(check.Detail)
		writeLine(w, line)
		if check.Fix != "" && check.Status != string(agent.StatusOK) {
			writeLine(w, "    ", styleMuted.Render("→ "+check.Fix))
		}
	}
	writeLine(w)
	if r.OK {
		writeLine(w, styleOK.Render("Tudo certo."), styleMuted.Render(" Avisos não são falhas."))
	} else {
		writeLine(w, styleFail.Render("Há falhas."), styleMuted.Render(" Detalhes: herdr-recall service doctor"))
	}
}
