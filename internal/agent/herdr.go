package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"herdr-recall/internal/contract"
)

const herdrTimeout = 5 * time.Second

// CaptureOptions is one local turn. It stops after the files are synced.
type CaptureOptions struct {
	StateDir string
	Host     string
	BootID   string
	Now      time.Time
	Herdr    func(ctx context.Context) ([]byte, error)
	NewID    func() (string, error)
	DirSync  func(dir string) error
	GOOS     string
}

// HerdrArgv is the only Herdr command the capture runs.
func HerdrArgv() []string {
	return []string{"herdr", "api", "snapshot"}
}

// RunHerdrSnapshot runs that command with a fixed argument list and no shell.
func RunHerdrSnapshot(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, herdrTimeout)
	defer cancel()
	argv := HerdrArgv()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = nil
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// Capture reads one Herdr snapshot, appends this turn to the spool, and syncs
// the files. It does not open a network connection.
func Capture(ctx context.Context, opt CaptureOptions) error {
	if opt.StateDir == "" {
		return errors.New("diretório de estado vazio")
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	now := opt.Now.UTC()
	if opt.Herdr == nil {
		opt.Herdr = RunHerdrSnapshot
	}
	if opt.NewID == nil {
		opt.NewID = newSnapshotID
	}
	if opt.DirSync == nil {
		opt.DirSync = syncDir
	}
	goos := opt.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}

	spoolPath, latestPath := statePaths(opt.StateDir)
	spool, err := loadSpool(spoolPath)
	if err != nil {
		return err
	}
	baseline, haveBaseline, err := baselineDesk(spool, latestPath)
	if err != nil {
		return err
	}

	raw, herdrErr := opt.Herdr(ctx)
	panes := []contract.Pane{}
	herdrOK := false
	var layouts []contract.Layout
	if herdrErr == nil {
		parsed, parsedLayouts, parseErr := ParseSnapshot(raw)
		if parseErr == nil {
			panes = parsed
			layouts = parsedLayouts
			herdrOK = true
		}
	}

	paneCount := 0
	if herdrOK {
		paneCount = len(panes)
	} else if haveBaseline {
		paneCount = len(baseline.Panes)
	}
	if paneCount > 128 {
		paneCount = 128
	}

	observed := contract.FormatTime(now)
	presenceBody, err := contract.MarshalPresence(contract.Presence{
		BootID:     opt.BootID,
		ObservedAt: observed,
		PaneCount:  paneCount,
		HerdrOK:    herdrOK,
	})
	if err != nil {
		return err
	}
	if _, err := contract.ValidatePresence(presenceBody); err != nil {
		return err
	}

	tail := []spoolItem{{
		Kind:       kindPresence,
		ObservedAt: observed,
		Body:       presenceBody,
	}}
	wroteSnapshot := false
	var snapshotBody []byte
	if herdrOK {
		changed := !haveBaseline
		if haveBaseline {
			var changedErr error
			changed, changedErr = deskChanged(baseline.Panes, baseline.Layouts, panes, layouts)
			if changedErr != nil {
				return changedErr
			}
		}
		if changed {
			id, idErr := opt.NewID()
			if idErr != nil {
				return idErr
			}
			body, marshalErr := contract.MarshalSnapshot(contract.Snapshot{
				SnapshotID: id,
				Host:       opt.Host,
				BootID:     opt.BootID,
				CapturedAt: observed,
				Panes:      panes,
				Layouts:    layouts,
			})
			if marshalErr != nil {
				return marshalErr
			}
			if _, validateErr := contract.ValidateSnapshot(body); validateErr != nil {
				return validateErr
			}
			snapshotBody = body
			tail = append(tail, spoolItem{
				Kind:       kindSnapshot,
				SnapshotID: id,
				Body:       body,
			})
			wroteSnapshot = true
		}
	}

	spool.Items = append(spool.Items, tail...)
	if err := saveSpool(spoolPath, spool, opt.DirSync); err != nil {
		if kept := continueAfterDirSync(err, goos, opt.StateDir, now); kept != nil {
			return kept
		}
	}
	if wroteSnapshot {
		return writeDesk(opt.StateDir, latestPath, now, snapshotBody, opt.DirSync, goos)
	}
	if !herdrOK {
		return nil
	}
	body, ok, err := matchingSnapshotBody(spool, panes, layouts)
	if err != nil || !ok {
		return err
	}
	current, readErr := os.ReadFile(latestPath)
	if readErr == nil && bytes.Equal(current, body) {
		return backfillHistory(opt.StateDir, now, body, opt.DirSync, goos)
	}
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	return writeDesk(opt.StateDir, latestPath, now, body, opt.DirSync, goos)
}

func backfillHistory(stateDir string, now time.Time, body []byte, dirSync func(string) error, goos string) error {
	var probe struct {
		SnapshotID string `json:"snapshot_id"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return err
	}
	day := historyTime(body, now).UTC().Format("20060102")
	raw, err := os.ReadFile(filepath.Join(stateDir, "history", day+".jsonl"))
	if err == nil && bytes.Contains(raw, []byte(probe.SnapshotID)) {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := appendHistory(stateDir, historyTime(body, now), now, body, dirSync); err != nil {
		if kept := continueAfterDirSync(err, goos, stateDir, now); kept != nil {
			return kept
		}
	}
	return nil
}

func writeDesk(stateDir, latestPath string, now time.Time, body []byte, dirSync func(string) error, goos string) error {
	if err := atomicWrite(latestPath, body, dirSync); err != nil {
		if kept := continueAfterDirSync(err, goos, stateDir, now); kept != nil {
			return kept
		}
	}
	if err := appendHistory(stateDir, historyTime(body, now), now, body, dirSync); err != nil {
		if kept := continueAfterDirSync(err, goos, stateDir, now); kept != nil {
			return kept
		}
	}
	return nil
}

func isDirSync(err error) bool {
	var dirErr *dirSyncError
	return errors.As(err, &dirErr)
}

// continueAfterDirSync records a Darwin directory-sync refusal and lets the turn finish.
// Linux keeps the error: the renamed file stays, and this turn does not continue.
func continueAfterDirSync(err error, goos, stateDir string, now time.Time) error {
	if err == nil {
		return nil
	}
	if !isDirSync(err) || goos != "darwin" {
		return err
	}
	if warnErr := writeDirSyncWarn(stateDir, now); warnErr != nil {
		return warnErr
	}
	return nil
}

// ParseSnapshot keeps the pane cwd, a kind=id session, and the split tree.
// Rectangles, scrollback, titles, and every other Herdr field are dropped.
func ParseSnapshot(raw []byte) ([]contract.Pane, []contract.Layout, error) {
	var envelope herdrEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, nil, err
	}
	snap := envelope.Result.Snapshot
	if snap.Panes == nil || snap.Workspaces == nil || snap.Tabs == nil {
		return nil, nil, errors.New("mesa ausente")
	}
	if len(snap.Panes) > 128 {
		return nil, nil, errors.New("mesa grande demais")
	}
	workspaces := map[string]string{}
	for _, workspace := range snap.Workspaces {
		workspaces[workspace.ID] = clipLabel(workspace.Label)
	}
	tabs := map[string]string{}
	for _, tab := range snap.Tabs {
		tabs[tab.ID] = clipLabel(tab.Label)
	}
	panes := make([]contract.Pane, 0, len(snap.Panes))
	seen := map[string]struct{}{}
	for _, pane := range snap.Panes {
		if pane.PaneID == "" || pane.WorkspaceID == "" || pane.TabID == "" {
			return nil, nil, errors.New("pane sem id")
		}
		if _, ok := seen[pane.PaneID]; ok {
			return nil, nil, errors.New("pane repetido")
		}
		seen[pane.PaneID] = struct{}{}
		cwd := ""
		if pane.Cwd != nil {
			cwd = cleanCwd(*pane.Cwd)
		}
		built := contract.Pane{
			WorkspaceID:    pane.WorkspaceID,
			WorkspaceLabel: workspaces[pane.WorkspaceID],
			TabID:          pane.TabID,
			TabLabel:       tabs[pane.TabID],
			PaneID:         pane.PaneID,
			PaneLabel:      clipLabel(pane.Label),
			Cwd:            cwd,
			MatchBasis:     contract.MatchNone,
			Confidence:     contract.ConfidenceLow,
		}
		applySession(&built, pane.AgentSession)
		panes = append(panes, built)
	}
	markAmbiguity(panes)
	layouts, err := buildLayouts(snap)
	if err != nil {
		return nil, nil, err
	}
	return panes, layouts, nil
}

func applySession(pane *contract.Pane, session *herdrSession) {
	if session == nil {
		return
	}
	id, ok := contract.SessionFromAgent(session.Kind, session.Value)
	if !ok {
		return
	}
	harness := strings.ToLower(strings.TrimSpace(session.Agent))
	argv := resumeArgv(harness, id)
	if argv == nil {
		return
	}
	pane.HerdrSession = id
	pane.Harness = harness
	pane.SessionID = id
	pane.ResumeArgv = argv
	pane.MatchBasis = contract.MatchHerdrAgentSession
	pane.Confidence = contract.ConfidenceHigh
}

func resumeArgv(harness, id string) []string {
	switch harness {
	case contract.HarnessClaude, contract.HarnessGrok:
		return []string{harness, "--resume", id}
	case contract.HarnessOpenCode:
		return []string{contract.HarnessOpenCode, "--session", id}
	default:
		return nil
	}
}

func markAmbiguity(panes []contract.Pane) {
	sessions := map[string][]string{}
	for _, pane := range panes {
		if pane.SessionID == "" {
			continue
		}
		sessions[pane.SessionID] = append(sessions[pane.SessionID], pane.PaneID)
	}
	for i, pane := range panes {
		if pane.SessionID == "" {
			continue
		}
		ids := sessions[pane.SessionID]
		if len(ids) < 2 {
			continue
		}
		others := make([]string, 0, len(ids)-1)
		for _, id := range ids {
			if id == pane.PaneID {
				continue
			}
			others = append(others, id)
		}
		slices.Sort(others)
		panes[i].AmbiguousWith = others
		panes[i].Confidence = contract.ConfidenceLow
	}
}

func clipLabel(value string) string {
	if utf8.RuneCountInString(value) <= 80 {
		return value
	}
	return string([]rune(value)[:80])
}

func cleanCwd(value string) string {
	if value == "" {
		return ""
	}
	if len(value) > 1024 || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func newSnapshotID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func deskChanged(leftPanes []contract.Pane, leftLayouts []contract.Layout, rightPanes []contract.Pane, rightLayouts []contract.Layout) (bool, error) {
	leftHash, err := deskHash(leftPanes, leftLayouts)
	if err != nil {
		return false, err
	}
	rightHash, err := deskHash(rightPanes, rightLayouts)
	if err != nil {
		return false, err
	}
	return leftHash != rightHash, nil
}

func deskHash(panes []contract.Pane, layouts []contract.Layout) (string, error) {
	return contract.SemanticHash(contract.Snapshot{
		SnapshotID: strings.Repeat("a", 16),
		Host:       "desk",
		BootID:     "boot",
		CapturedAt: "2026-11-13T00:00:00.000000000Z",
		Panes:      panes,
		Layouts:    layouts,
	})
}

type herdrEnvelope struct {
	Result struct {
		Snapshot herdrSnapshot `json:"snapshot"`
	} `json:"result"`
}

type herdrSnapshot struct {
	Workspaces []herdrWorkspace `json:"workspaces"`
	Tabs       []herdrTab       `json:"tabs"`
	Panes      []herdrPane      `json:"panes"`
	Layouts    []herdrLayout    `json:"layouts"`
}

type herdrWorkspace struct {
	ID     string `json:"workspace_id"`
	Label  string `json:"label"`
	Number int    `json:"number"`
}

type herdrTab struct {
	ID          string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
	Number      int    `json:"number"`
}

type herdrLayout struct {
	WorkspaceID string             `json:"workspace_id"`
	TabID       string             `json:"tab_id"`
	Zoomed      bool               `json:"zoomed"`
	Area        herdrRect          `json:"area"`
	Panes       []herdrLayoutPane  `json:"panes"`
	Splits      []herdrLayoutSplit `json:"splits"`
}

type herdrLayoutPane struct {
	PaneID string    `json:"pane_id"`
	Rect   herdrRect `json:"rect"`
}

type herdrLayoutSplit struct {
	Direction string    `json:"direction"`
	Ratio     float64   `json:"ratio"`
	Rect      herdrRect `json:"rect"`
}

type herdrRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (r herdrRect) toRect() cellRect {
	return cellRect{X: r.X, Y: r.Y, Width: r.Width, Height: r.Height}
}

type herdrPane struct {
	PaneID       string        `json:"pane_id"`
	WorkspaceID  string        `json:"workspace_id"`
	TabID        string        `json:"tab_id"`
	Label        string        `json:"label"`
	Cwd          *string       `json:"cwd"`
	AgentSession *herdrSession `json:"agent_session"`
}

type herdrSession struct {
	Agent string `json:"agent"`
	Kind  string `json:"kind"`
	Value string `json:"value"`
}
