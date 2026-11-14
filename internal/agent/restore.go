package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"herdr-recall/internal/contract"
)

const agentStartTimeoutMS = "60000"

var (
	ErrDefaultSession  = errors.New("sessão default")
	ErrSessionName     = errors.New("nome de sessão")
	ErrNoLayout        = errors.New("divisão ausente")
	ErrSessionOccupied = errors.New("sessão ocupada")
	ErrHerdr           = errors.New("herdr sem resposta")
	ErrRestore         = errors.New("montagem falhou")
)

// Runner executes one Herdr argv. It does not use a shell.
type Runner func(ctx context.Context, argv []string) ([]byte, error)

// RestoreReport is what the command prints. Argv is only for the apply path's log count.
type RestoreReport struct {
	Lines      []string
	Workspaces int
	Panes      int
	Plan       bool
}

// Restore rebuilds a saved desk into an empty named Herdr session.
// Plan reads the session and returns the lines without creating a pane.
func Restore(ctx context.Context, session string, desk contract.Snapshot, plan bool, run Runner) (RestoreReport, error) {
	report := RestoreReport{Plan: plan, Panes: len(desk.Panes)}
	if err := RejectSession(session); err != nil {
		return report, err
	}
	if len(desk.Layouts) == 0 {
		return report, ErrNoLayout
	}
	if run == nil {
		return report, ErrHerdr
	}
	raw, err := run(ctx, herdrArgv(session, "api", "snapshot"))
	if err != nil {
		return report, ErrHerdr
	}
	occupied, err := sessionOccupied(raw)
	if err != nil {
		return report, ErrHerdr
	}
	if occupied {
		return report, ErrSessionOccupied
	}
	groups := workspaceGroups(desk)
	report.Workspaces = len(groups)
	if plan {
		report.Lines = planLines(desk, groups)
		return report, nil
	}
	if err := applyDesk(ctx, session, desk, groups, run); err != nil {
		return report, err
	}
	return report, nil
}

// RejectSession refuses the default session and names Herdr would treat as flags.
func RejectSession(name string) error {
	if name == "default" {
		return ErrDefaultSession
	}
	if !sessionNameOK(name) {
		return ErrSessionName
	}
	return nil
}

func sessionNameOK(name string) bool {
	count := utf8.RuneCountInString(name)
	if count < 1 || count > 64 {
		return false
	}
	for i, r := range name {
		if r > unicode.MaxASCII {
			return false
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		if (r == '-' || r == '_') && i > 0 {
			continue
		}
		return false
	}
	return true
}

func sessionOccupied(raw []byte) (bool, error) {
	var envelope struct {
		Result struct {
			Snapshot struct {
				Workspaces []struct {
					ID string `json:"workspace_id"`
				} `json:"workspaces"`
			} `json:"snapshot"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false, err
	}
	return len(envelope.Result.Snapshot.Workspaces) > 0, nil
}

type workspaceGroup struct {
	Label   string
	Layouts []contract.Layout
}

func workspaceGroups(desk contract.Snapshot) []workspaceGroup {
	var groups []workspaceGroup
	for _, layout := range desk.Layouts {
		if len(groups) == 0 || groups[len(groups)-1].Layouts[0].WorkspaceID != layout.WorkspaceID {
			groups = append(groups, workspaceGroup{Label: workspaceLabel(desk, layout.WorkspaceID)})
		}
		last := &groups[len(groups)-1]
		last.Layouts = append(last.Layouts, layout)
	}
	return groups
}

func planLines(desk contract.Snapshot, groups []workspaceGroup) []string {
	var lines []string
	for _, group := range groups {
		lines = append(lines, "workspace "+quote(group.Label)+" "+quote(originCwd(desk, group.Layouts[0].Root)))
		for _, layout := range group.Layouts {
			lines = append(lines, "aba "+quote(tabLabel(desk, layout.TabID))+" "+quote(originCwd(desk, layout.Root)))
			lines = append(lines, planSplits(desk, layout.Root)...)
			if layout.Zoomed {
				lines = append(lines, "zoom")
			}
		}
	}
	for _, layout := range desk.Layouts {
		lines = append(lines, harnessLines(desk, layout.Root)...)
	}
	return lines
}

func planSplits(desk contract.Snapshot, node *contract.Node) []string {
	if node == nil || node.Type != contract.NodeSplit {
		return nil
	}
	var lines []string
	lines = append(lines, "dividir "+node.Direction+" "+strconv.FormatFloat(node.Ratio, 'f', -1, 64)+" "+quote(originCwd(desk, node.Second)))
	lines = append(lines, planSplits(desk, node.First)...)
	lines = append(lines, planSplits(desk, node.Second)...)
	return lines
}

func harnessLines(desk contract.Snapshot, node *contract.Node) []string {
	if node == nil {
		return nil
	}
	if node.Type == contract.NodePane {
		pane, ok := paneByID(desk, node.PaneID)
		if !ok || !startable(pane) {
			return nil
		}
		return []string{"harness " + quoteArgv(pane.ResumeArgv)}
	}
	var lines []string
	lines = append(lines, harnessLines(desk, node.First)...)
	lines = append(lines, harnessLines(desk, node.Second)...)
	return lines
}

func applyDesk(ctx context.Context, session string, desk contract.Snapshot, groups []workspaceGroup, run Runner) error {
	created := map[string]string{}
	agents := 0
	for _, group := range groups {
		first := group.Layouts[0]
		workspaceID, tabID, rootID, err := createWorkspace(ctx, session, group.Label, originCwd(desk, first.Root), run)
		if err != nil {
			return err
		}
		if err := renameTab(ctx, session, tabID, tabLabel(desk, first.TabID), run); err != nil {
			return err
		}
		if err := applyNode(ctx, session, desk, first.Root, rootID, created, run); err != nil {
			return err
		}
		if first.Zoomed {
			if err := zoom(ctx, session, created[originPane(first.Root)], run); err != nil {
				return err
			}
		}
		for _, layout := range group.Layouts[1:] {
			_, rootID, err := createTab(ctx, session, workspaceID, tabLabel(desk, layout.TabID), originCwd(desk, layout.Root), run)
			if err != nil {
				return err
			}
			if err := applyNode(ctx, session, desk, layout.Root, rootID, created, run); err != nil {
				return err
			}
			if layout.Zoomed {
				if err := zoom(ctx, session, created[originPane(layout.Root)], run); err != nil {
					return err
				}
			}
		}
	}
	for _, layout := range desk.Layouts {
		var err error
		agents, err = startAgents(ctx, session, desk, layout.Root, created, agents, run)
		if err != nil {
			return err
		}
	}
	return nil
}

func applyNode(ctx context.Context, session string, desk contract.Snapshot, node *contract.Node, paneID string, created map[string]string, run Runner) error {
	if node == nil || paneID == "" {
		return ErrRestore
	}
	if node.Type == contract.NodePane {
		created[node.PaneID] = paneID
		pane, ok := paneByID(desk, node.PaneID)
		if ok {
			return renamePane(ctx, session, paneID, pane.PaneLabel, run)
		}
		return nil
	}
	if node.Type != contract.NodeSplit {
		return ErrRestore
	}
	argv := herdrArgv(session, "pane", "split", "--pane", paneID, "--direction", node.Direction, "--ratio", strconv.FormatFloat(node.Ratio, 'f', -1, 64))
	cwd := originCwd(desk, node.Second)
	if cwd != "" {
		argv = append(argv, "--cwd", cwd)
	}
	argv = append(argv, "--no-focus")
	raw, err := run(ctx, argv)
	if err != nil {
		return ErrRestore
	}
	newPane, err := resultString(raw, "pane", "pane_id")
	if err != nil || newPane == "" {
		return ErrRestore
	}
	if err := applyNode(ctx, session, desk, node.First, paneID, created, run); err != nil {
		return err
	}
	return applyNode(ctx, session, desk, node.Second, newPane, created, run)
}

func createWorkspace(ctx context.Context, session string, label string, cwd string, run Runner) (string, string, string, error) {
	argv := herdrArgv(session, "workspace", "create")
	if label != "" {
		argv = append(argv, "--label", label)
	}
	if cwd != "" {
		argv = append(argv, "--cwd", cwd)
	}
	argv = append(argv, "--no-focus")
	raw, err := run(ctx, argv)
	if err != nil {
		return "", "", "", ErrRestore
	}
	workspaceID, err := resultString(raw, "workspace", "workspace_id")
	if err != nil || workspaceID == "" {
		return "", "", "", ErrRestore
	}
	tabID, err := resultString(raw, "tab", "tab_id")
	if err != nil || tabID == "" {
		return "", "", "", ErrRestore
	}
	rootID, err := resultString(raw, "root_pane", "pane_id")
	if err != nil || rootID == "" {
		return "", "", "", ErrRestore
	}
	return workspaceID, tabID, rootID, nil
}

func createTab(ctx context.Context, session string, workspaceID string, label string, cwd string, run Runner) (string, string, error) {
	argv := herdrArgv(session, "tab", "create", "--workspace", workspaceID)
	if label != "" {
		argv = append(argv, "--label", label)
	}
	if cwd != "" {
		argv = append(argv, "--cwd", cwd)
	}
	argv = append(argv, "--no-focus")
	raw, err := run(ctx, argv)
	if err != nil {
		return "", "", ErrRestore
	}
	tabID, err := resultString(raw, "tab", "tab_id")
	if err != nil || tabID == "" {
		return "", "", ErrRestore
	}
	rootID, err := resultString(raw, "root_pane", "pane_id")
	if err != nil || rootID == "" {
		return "", "", ErrRestore
	}
	return tabID, rootID, nil
}

func renameTab(ctx context.Context, session string, tabID string, label string, run Runner) error {
	if label == "" {
		return nil
	}
	if _, err := run(ctx, herdrArgv(session, "tab", "rename", tabID, label)); err != nil {
		return ErrRestore
	}
	return nil
}

func renamePane(ctx context.Context, session string, paneID string, label string, run Runner) error {
	if label == "" {
		return nil
	}
	if _, err := run(ctx, herdrArgv(session, "pane", "rename", paneID, label)); err != nil {
		return ErrRestore
	}
	return nil
}

func zoom(ctx context.Context, session string, paneID string, run Runner) error {
	if paneID == "" {
		return ErrRestore
	}
	if _, err := run(ctx, herdrArgv(session, "pane", "zoom", "--pane", paneID, "--on")); err != nil {
		return ErrRestore
	}
	return nil
}

func startAgents(ctx context.Context, session string, desk contract.Snapshot, node *contract.Node, created map[string]string, started int, run Runner) (int, error) {
	if node == nil {
		return started, ErrRestore
	}
	if node.Type == contract.NodePane {
		pane, ok := paneByID(desk, node.PaneID)
		if !ok || !startable(pane) {
			return started, nil
		}
		started++
		name := agentName(pane.Harness, started)
		target := created[node.PaneID]
		if name == "" || target == "" {
			return started, ErrRestore
		}
		argv := herdrArgv(session, "agent", "start", name, "--kind", pane.Harness, "--pane", target, "--timeout", agentStartTimeoutMS, "--")
		argv = append(argv, pane.ResumeArgv[1:]...)
		if _, err := run(ctx, argv); err != nil {
			return started, ErrRestore
		}
		return started, nil
	}
	var err error
	started, err = startAgents(ctx, session, desk, node.First, created, started, run)
	if err != nil {
		return started, err
	}
	return startAgents(ctx, session, desk, node.Second, created, started, run)
}

func startable(pane contract.Pane) bool {
	if pane.Confidence != contract.ConfidenceHigh || len(pane.AmbiguousWith) > 0 {
		return false
	}
	return contract.AllowArgv(pane.Harness, pane.ResumeArgv)
}

func agentName(kind string, n int) string {
	if n < 1 || n > 128 {
		return ""
	}
	name := kind + strconv.Itoa(n)
	if len(name) > 32 || kind == "" {
		return ""
	}
	for i, r := range name {
		if i == 0 && (r < 'a' || r > 'z') {
			return ""
		}
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return ""
		}
	}
	return name
}

func herdrArgv(session string, args ...string) []string {
	argv := []string{"herdr", "--session", session}
	return append(argv, args...)
}

func resultString(raw []byte, object string, field string) (string, error) {
	var envelope struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", err
	}
	body, ok := envelope.Result[object]
	if !ok {
		return "", errors.New("resposta sem id")
	}
	var values map[string]any
	if err := json.Unmarshal(body, &values); err != nil {
		return "", err
	}
	text, ok := values[field].(string)
	if !ok {
		return "", errors.New("resposta sem id")
	}
	return text, nil
}

func originCwd(desk contract.Snapshot, node *contract.Node) string {
	id := originPane(node)
	pane, ok := paneByID(desk, id)
	if !ok {
		return ""
	}
	return pane.Cwd
}

func originPane(node *contract.Node) string {
	for node != nil && node.Type == contract.NodeSplit {
		node = node.First
	}
	if node == nil {
		return ""
	}
	return node.PaneID
}

func paneByID(desk contract.Snapshot, id string) (contract.Pane, bool) {
	for _, pane := range desk.Panes {
		if pane.PaneID == id {
			return pane, true
		}
	}
	return contract.Pane{}, false
}

func workspaceLabel(desk contract.Snapshot, id string) string {
	for _, pane := range desk.Panes {
		if pane.WorkspaceID == id {
			return pane.WorkspaceLabel
		}
	}
	return ""
}

func tabLabel(desk contract.Snapshot, id string) string {
	for _, pane := range desk.Panes {
		if pane.TabID == id {
			return pane.TabLabel
		}
	}
	return ""
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func quoteArgv(argv []string) string {
	parts := make([]string, len(argv))
	for i, arg := range argv {
		parts[i] = quote(arg)
	}
	return strings.Join(parts, " ")
}
