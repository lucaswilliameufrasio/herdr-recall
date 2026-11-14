package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"herdr-recall/internal/contract"
)

func TestRunHerdrSnapshotUsesFixedArgv(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HERDR_ARGS\"\nprintf '%s' '" + emptyHerdr() + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HERDR_ARGS", argsPath)
	if _, err := RunHerdrSnapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "api\nsnapshot\n" {
		t.Fatalf("argv: %q", got)
	}
	if strings.Join(HerdrArgv(), " ") != "herdr api snapshot" {
		t.Fatalf("argv fixo: %v", HerdrArgv())
	}
}

func TestCaptureKeepsRestoreFieldsAndDropsTheRest(t *testing.T) {
	dir := t.TempDir()
	when := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	ids := 0
	opt := testOptions(dir, herdrRaw(pathPane+","+sessionPane), when)
	opt.NewID = func() (string, error) {
		ids++
		if ids > 1 {
			t.Fatal("snapshot_id recriado")
		}
		return "0123456789abcdef", nil
	}
	if err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}

	latest := readLatest(t, dir)
	if latest.SnapshotID != "0123456789abcdef" {
		t.Fatalf("id: %s", latest.SnapshotID)
	}
	if latest.CapturedAt != "2026-11-13T00:00:00.000000000Z" {
		t.Fatalf("hora: %s", latest.CapturedAt)
	}
	if len(latest.Panes) != 2 {
		t.Fatalf("panes: %d", len(latest.Panes))
	}
	path := latest.Panes[0]
	if path.PaneID != "p0" || path.Cwd != "/tmp/a<b" || path.MatchBasis != contract.MatchNone || path.SessionID != "" || path.ResumeArgv != nil {
		t.Fatalf("pane sem sessão: %+v", path)
	}
	session := latest.Panes[1]
	if session.Harness != contract.HarnessOpenCode || session.SessionID != "ses_abcdef12" || session.HerdrSession != "ses_abcdef12" {
		t.Fatalf("sessão: %+v", session)
	}
	if session.Confidence != contract.ConfidenceHigh || session.MatchBasis != contract.MatchHerdrAgentSession {
		t.Fatalf("confiança: %+v", session)
	}
	if strings.Join(session.ResumeArgv, " ") != "opencode --session ses_abcdef12" {
		t.Fatalf("argv: %v", session.ResumeArgv)
	}
	if len(latest.Layouts) != 1 || latest.Layouts[0].Root == nil || latest.Layouts[0].Root.Type != contract.NodeSplit {
		t.Fatalf("divisão: %+v", latest.Layouts)
	}
	if latest.Layouts[0].Root.Direction != contract.SplitRight || latest.Layouts[0].Root.First.PaneID != "p0" || latest.Layouts[0].Root.Second.PaneID != "p1" {
		t.Fatalf("árvore: %+v", latest.Layouts[0].Root)
	}

	raw := mustRead(t, filepath.Join(dir, "latest.json"))
	spoolRaw := mustRead(t, filepath.Join(dir, "spool.json"))
	for _, forbidden := range []string{"foreground-not-stored", "secret-title", "do not store", "/tmp/not-a-session", `"scroll"`, `"prompt"`, `"tokens"`, `"foreground_cwd"`, `"rect"`} {
		if strings.Contains(string(raw), forbidden) || strings.Contains(string(spoolRaw), forbidden) {
			t.Fatalf("campo do Herdr vazou: %s", forbidden)
		}
	}
	if !strings.Contains(string(raw), `/tmp/a<b`) {
		t.Fatalf("cwd escapado: %s", raw)
	}
	if info, err := os.Stat(filepath.Join(dir, "latest.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("modo do latest: %v %v", info, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "history")); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("modo do diretório: %v %v", info, err)
	}

	spool := readSpoolFile(t, dir)
	if len(spool.Items) != 2 || spool.Items[0].Kind != kindPresence || spool.Items[1].Kind != kindSnapshot {
		t.Fatalf("ordem do spool: %+v", spool.Items)
	}
	if spool.Items[1].SnapshotID != "0123456789abcdef" {
		t.Fatalf("id do spool: %s", spool.Items[1].SnapshotID)
	}
	history := mustRead(t, filepath.Join(dir, "history", "20261113.jsonl"))
	if !strings.Contains(string(history), "0123456789abcdef") {
		t.Fatalf("histórico: %s", history)
	}

	latestInfo, err := os.Stat(filepath.Join(dir, "latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	opt.Now = when.Add(time.Minute)
	if err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	if ids != 1 {
		t.Fatalf("ids numa mesa quieta: %d", ids)
	}
	againInfo, err := os.Stat(filepath.Join(dir, "latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !latestInfo.ModTime().Equal(againInfo.ModTime()) || string(mustRead(t, filepath.Join(dir, "latest.json"))) != string(raw) {
		t.Fatal("mesa quieta reescreveu latest.json")
	}
	spool = readSpoolFile(t, dir)
	if len(spool.Items) != 3 || spool.Items[0].ObservedAt != "2026-11-13T00:00:00.000000000Z" || spool.Items[2].Kind != kindPresence {
		t.Fatalf("cabeça do spool mudou: %+v", spool.Items)
	}

	opt.Now = when.Add(2 * time.Minute)
	opt.Herdr = func(context.Context) ([]byte, error) {
		return nil, errors.New("herdr parado")
	}
	if err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, filepath.Join(dir, "latest.json"))) != string(raw) {
		t.Fatal("falha do Herdr zerou latest.json")
	}
	spool = readSpoolFile(t, dir)
	if len(spool.Items) != 4 || spool.Items[3].Kind != kindPresence {
		t.Fatalf("presença da falha: %+v", spool.Items)
	}
	presence, err := contract.ValidatePresence(spool.Items[3].Body)
	if err != nil {
		t.Fatal(err)
	}
	if presence.HerdrOK || presence.PaneCount != 2 {
		t.Fatalf("presença: %+v", presence)
	}
}

func TestEmptyDeskIsAnArray(t *testing.T) {
	dir := t.TempDir()
	opt := testOptions(dir, []byte(emptyHerdr()), time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	opt.NewID = func() (string, error) { return "0123456789abcdef", nil }
	if err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	raw := string(mustRead(t, filepath.Join(dir, "latest.json")))
	if !strings.Contains(raw, `"panes":[]`) || strings.Contains(raw, `"panes":null`) {
		t.Fatalf("mesa vazia: %s", raw)
	}

	opt.Herdr = func(context.Context) ([]byte, error) {
		return []byte(strings.Replace(emptyHerdr(), `"panes":[]`, `"panes":null`, 1)), nil
	}
	opt.Now = opt.Now.Add(time.Minute)
	if err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	if string(mustRead(t, filepath.Join(dir, "latest.json"))) != raw {
		t.Fatal("mesa nula substituiu a captura")
	}
}

func TestDuplicateSessionIsLowConfidence(t *testing.T) {
	dir := t.TempDir()
	raw := herdrRaw(strings.Replace(sessionPane, `"pane_id":"p1"`, `"pane_id":"p1"`, 1) + "," + strings.Replace(sessionPane, `"pane_id":"p1"`, `"pane_id":"p2"`, 1))
	opt := testOptions(dir, raw, time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	opt.NewID = func() (string, error) { return "0123456789abcdef", nil }
	if err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	latest := readLatest(t, dir)
	if len(latest.Panes) != 2 {
		t.Fatalf("panes: %d", len(latest.Panes))
	}
	for _, pane := range latest.Panes {
		if pane.Confidence != contract.ConfidenceLow || len(pane.ResumeArgv) != 3 || len(pane.AmbiguousWith) != 1 {
			t.Fatalf("ambíguo: %+v", pane)
		}
	}
	if latest.Panes[0].AmbiguousWith[0] != "p2" || latest.Panes[1].AmbiguousWith[0] != "p1" {
		t.Fatalf("pares: %+v %+v", latest.Panes[0].AmbiguousWith, latest.Panes[1].AmbiguousWith)
	}
}

func TestLongLabelIsClipped(t *testing.T) {
	dir := t.TempDir()
	label := strings.Repeat("a", 81)
	raw := herdrRaw(strings.Replace(pathPane, `"label":"outro"`, `"label":"`+label+`"`, 1))
	opt := testOptions(dir, raw, time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	opt.NewID = func() (string, error) { return "0123456789abcdef", nil }
	if err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	latest := readLatest(t, dir)
	if len([]rune(latest.Panes[0].PaneLabel)) != 80 {
		t.Fatalf("rótulo: %d", len([]rune(latest.Panes[0].PaneLabel)))
	}
}

func TestLinuxDirSyncFailureKeepsTheSnapshotID(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	opt := testOptions(dir, herdrRaw(sessionPane), time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	opt.GOOS = "linux"
	opt.DirSync = func(string) error { return errors.New("recusado") }
	opt.NewID = func() (string, error) {
		calls++
		if calls > 1 {
			t.Fatal("snapshot_id recriado")
		}
		return "0123456789abcdef", nil
	}
	if err := Capture(context.Background(), opt); err == nil {
		t.Fatal("o linux aceitou o sync recusado")
	}
	if _, err := os.Stat(filepath.Join(dir, "latest.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("latest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dirsync-warn")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("o linux gravou dirsync-warn")
	}
	opt.DirSync = nil
	opt.Now = opt.Now.Add(time.Minute)
	if err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("ids: %d", calls)
	}
	latest := readLatest(t, dir)
	if latest.SnapshotID != "0123456789abcdef" {
		t.Fatalf("id: %s", latest.SnapshotID)
	}
}

func TestDarwinDirSyncFailureWritesAWarning(t *testing.T) {
	dir := t.TempDir()
	opt := testOptions(dir, herdrRaw(sessionPane), time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	opt.GOOS = "darwin"
	opt.DirSync = func(string) error { return errors.New("recusado") }
	opt.NewID = func() (string, error) { return "0123456789abcdef", nil }
	if err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	warn := mustRead(t, filepath.Join(dir, "dirsync-warn"))
	if string(warn) != "2026-11-13T00:00:00.000000000Z Sync do diretório foi recusado\n" {
		t.Fatalf("aviso: %q", warn)
	}
	info, err := os.Stat(filepath.Join(dir, "dirsync-warn"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("modo: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "latest.json")); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryDropsOldDaysAndTheOldestFile(t *testing.T) {
	previousBytes := historyMaxBytes
	previousKeep := historyKeep
	historyMaxBytes = 200
	historyKeep = 48 * time.Hour
	t.Cleanup(func() {
		historyMaxBytes = previousBytes
		historyKeep = previousKeep
	})
	dir := t.TempDir()
	history := filepath.Join(dir, "history")
	old := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	fresh := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	if err := appendHistory(dir, old, fresh, historyLine("0123456789abcdef", old), syncDir); err != nil {
		t.Fatal(err)
	}
	if err := appendHistory(dir, fresh, fresh, historyLine("fedcba9876543210", fresh), syncDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(history, "20261110.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dia fora das 48 h ficou")
	}
	if _, err := os.Stat(filepath.Join(history, "20261113.jsonl")); err != nil {
		t.Fatal(err)
	}

	first := historyLine("aaaaaaaaaaaaaaaa", fresh)
	second := historyLine("bbbbbbbbbbbbbbbb", fresh.Add(time.Minute))
	if err := os.WriteFile(filepath.Join(history, "20261112.jsonl"), append(first, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(history, "20261113.jsonl"), append(second, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pruneHistory(history, fresh.Add(time.Minute), syncDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(history, "20261112.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("o dia mais antigo ficou acima do teto")
	}
	left := mustRead(t, filepath.Join(history, "20261113.jsonl"))
	if !strings.Contains(string(left), "bbbbbbbbbbbbbbbb") {
		t.Fatalf("sobrou: %s", left)
	}
}

func testOptions(dir string, raw []byte, now time.Time) CaptureOptions {
	return CaptureOptions{
		StateDir: dir,
		Host:     "desk",
		BootID:   "boot-1",
		Now:      now,
		Herdr: func(context.Context) ([]byte, error) {
			return raw, nil
		},
		GOOS: "linux",
	}
}

func herdrRaw(panes string) []byte {
	type row struct {
		PaneID      string `json:"pane_id"`
		WorkspaceID string `json:"workspace_id"`
		TabID       string `json:"tab_id"`
	}
	var rows []row
	if panes != "" {
		if err := json.Unmarshal([]byte("["+panes+"]"), &rows); err != nil {
			panic(err)
		}
	}
	type key struct {
		workspace string
		tab       string
	}
	var order []key
	grouped := map[key][]row{}
	for _, item := range rows {
		itemKey := key{item.WorkspaceID, item.TabID}
		if _, ok := grouped[itemKey]; !ok {
			order = append(order, itemKey)
		}
		grouped[itemKey] = append(grouped[itemKey], item)
	}
	type idLabel struct {
		ID     string `json:"workspace_id"`
		Label  string `json:"label"`
		Number int    `json:"number"`
	}
	type tabLabel struct {
		ID          string `json:"tab_id"`
		WorkspaceID string `json:"workspace_id"`
		Label       string `json:"label"`
		Number      int    `json:"number"`
	}
	type rect struct {
		X      int `json:"x"`
		Y      int `json:"y"`
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	type layoutPane struct {
		PaneID string `json:"pane_id"`
		Rect   rect   `json:"rect"`
	}
	type layoutSplit struct {
		Direction string  `json:"direction"`
		Ratio     float64 `json:"ratio"`
		Rect      rect    `json:"rect"`
	}
	type layout struct {
		WorkspaceID string        `json:"workspace_id"`
		TabID       string        `json:"tab_id"`
		Zoomed      bool          `json:"zoomed"`
		Area        rect          `json:"area"`
		Panes       []layoutPane  `json:"panes"`
		Splits      []layoutSplit `json:"splits"`
	}
	wsNumber := map[string]int{}
	tabNumber := map[string]int{}
	var workspaces []idLabel
	var tabs []tabLabel
	var layouts []layout
	for _, itemKey := range order {
		if _, ok := wsNumber[itemKey.workspace]; !ok {
			wsNumber[itemKey.workspace] = len(workspaces) + 1
			workspaces = append(workspaces, idLabel{ID: itemKey.workspace, Label: "mesa", Number: wsNumber[itemKey.workspace]})
		}
		tabNumber[itemKey.tab] = len(tabs) + 1
		tabs = append(tabs, tabLabel{ID: itemKey.tab, WorkspaceID: itemKey.workspace, Label: "aba", Number: tabNumber[itemKey.tab]})
		members := grouped[itemKey]
		panesOut := make([]layoutPane, len(members))
		var splits []layoutSplit
		for i, member := range members {
			panesOut[i] = layoutPane{PaneID: member.PaneID, Rect: rect{X: i * 10, Y: 0, Width: 10, Height: 20}}
		}
		for i := 0; i < len(members)-1; i++ {
			span := (len(members) - i) * 10
			splits = append(splits, layoutSplit{
				Direction: "right",
				Ratio:     10 / float64(span),
				Rect:      rect{X: i * 10, Y: 0, Width: span, Height: 20},
			})
		}
		areaWidth := 10
		if len(members) > 0 {
			areaWidth = len(members) * 10
		}
		layouts = append(layouts, layout{
			WorkspaceID: itemKey.workspace,
			TabID:       itemKey.tab,
			Area:        rect{Width: areaWidth, Height: 20},
			Panes:       panesOut,
			Splits:      splits,
		})
	}
	if workspaces == nil {
		workspaces = []idLabel{}
	}
	if tabs == nil {
		tabs = []tabLabel{}
	}
	if layouts == nil {
		layouts = []layout{}
	}
	workspaceJSON, err := json.Marshal(workspaces)
	if err != nil {
		panic(err)
	}
	tabJSON, err := json.Marshal(tabs)
	if err != nil {
		panic(err)
	}
	layoutJSON, err := json.Marshal(layouts)
	if err != nil {
		panic(err)
	}
	return []byte(`{"id":"req-1","result":{"type":"session_snapshot","snapshot":{"version":"t","protocol":22,"workspaces":` + string(workspaceJSON) + `,"tabs":` + string(tabJSON) + `,"panes":[` + panes + `],"layouts":` + string(layoutJSON) + `,"agents":[]}}}`)
}

func emptyHerdr() string {
	return string(herdrRaw(""))
}

func historyLine(id string, when time.Time) []byte {
	return []byte(`{"snapshot_id":"` + id + `","host":"desk","boot_id":"boot-1","captured_at":"` + contract.FormatTime(when) + `","panes":[]}`)
}

func readLatest(t *testing.T, dir string) contract.Snapshot {
	t.Helper()
	snap, err := contract.ValidateSnapshot(mustRead(t, filepath.Join(dir, "latest.json")))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func readSpoolFile(t *testing.T, dir string) spoolFile {
	t.Helper()
	file, err := loadSpool(filepath.Join(dir, "spool.json"))
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

const pathPane = `{"pane_id":"p0","workspace_id":"w1","tab_id":"t1","label":"outro","cwd":"/tmp/a<b","foreground_cwd":"/tmp/foreground-not-stored","terminal_title":"secret-title","scroll":{"offset_from_bottom":4},"tokens":{"secret":"value"},"prompt":"do not store","agent_session":{"agent":"claude","kind":"path","source":"agent","value":"/tmp/not-a-session"}}`

const sessionPane = `{"pane_id":"p1","workspace_id":"w1","tab_id":"t1","label":"pane","cwd":"/home/project","agent_session":{"agent":"OpenCode","kind":"id","source":"agent","value":"ses_abcdef12"}}`
