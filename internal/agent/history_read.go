package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"herdr-recall/internal/contract"
)

// HistoryEntry is one distinct desk kept in the local 48 h history.
type HistoryEntry struct {
	SnapshotID string
	BootID     string
	CapturedAt time.Time
	Panes      int
	Workspaces int
	Tabs       int
	Resumable  int
	raw        []byte
}

// Snapshot validates and returns the full desk of this entry.
func (h HistoryEntry) Snapshot() (contract.Snapshot, error) {
	return contract.ValidateSnapshot(h.raw)
}

// ReadHistory lists the local history, newest first. Lines that do not parse
// are skipped: the history is a convenience copy, latest.json is the source.
func ReadHistory(stateDir string) ([]HistoryEntry, error) {
	dir := filepath.Join(stateDir, "history")
	names, err := historyFiles(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var entries []HistoryEntry
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		for _, line := range splitLines(raw) {
			entry, ok := historyEntry(line)
			if !ok || seen[entry.SnapshotID] {
				continue
			}
			seen[entry.SnapshotID] = true
			entries = append(entries, entry)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].CapturedAt.After(entries[j].CapturedAt)
	})
	return entries, nil
}

func historyEntry(line []byte) (HistoryEntry, bool) {
	var snap contract.Snapshot
	if err := json.Unmarshal(line, &snap); err != nil || snap.SnapshotID == "" {
		return HistoryEntry{}, false
	}
	at, ok := contract.ParseTime(snap.CapturedAt)
	if !ok {
		return HistoryEntry{}, false
	}
	workspaces := map[string]bool{}
	tabs := map[string]bool{}
	resumable := 0
	for _, pane := range snap.Panes {
		workspaces[pane.WorkspaceID] = true
		tabs[pane.TabID] = true
		if len(pane.ResumeArgv) > 0 {
			resumable++
		}
	}
	raw := make([]byte, len(line))
	copy(raw, line)
	return HistoryEntry{
		SnapshotID: snap.SnapshotID,
		BootID:     snap.BootID,
		CapturedAt: at,
		Panes:      len(snap.Panes),
		Workspaces: len(workspaces),
		Tabs:       len(tabs),
		Resumable:  resumable,
		raw:        raw,
	}, true
}

// QueueEntry describes one spool item without its body.
type QueueEntry struct {
	Kind       string
	SnapshotID string
	ObservedAt string
	Bytes      int
}

// QueueState is a point-in-time read of the spool and the quarantine.
type QueueState struct {
	Pending   []QueueEntry
	Conflicts []QueueEntry
}

// ReadQueue reads spool.json and spool.conflict without locking: the numbers
// are a snapshot and may change while the service is sending.
func ReadQueue(stateDir string) (QueueState, error) {
	pending, err := loadSpool(filepath.Join(stateDir, "spool.json"))
	if err != nil {
		return QueueState{}, err
	}
	conflicts, err := loadSpool(filepath.Join(stateDir, "spool.conflict"))
	if err != nil {
		return QueueState{}, err
	}
	return QueueState{Pending: queueEntries(pending), Conflicts: queueEntries(conflicts)}, nil
}

func queueEntries(file spoolFile) []QueueEntry {
	out := make([]QueueEntry, 0, len(file.Items))
	for _, item := range file.Items {
		entry := QueueEntry{Kind: item.Kind, SnapshotID: item.SnapshotID, ObservedAt: item.ObservedAt, Bytes: len(item.Body)}
		if entry.Kind == kindSnapshot && entry.ObservedAt == "" {
			var probe struct {
				CapturedAt string `json:"captured_at"`
			}
			if json.Unmarshal(item.Body, &probe) == nil {
				entry.ObservedAt = probe.CapturedAt
			}
		}
		out = append(out, entry)
	}
	return out
}
