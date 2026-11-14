package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"herdr-recall/internal/contract"
)

const (
	kindPresence = "presence"
	kindSnapshot = "snapshot"
)

type spoolFile struct {
	Items []spoolItem `json:"items"`
}

type spoolItem struct {
	Kind       string          `json:"kind"`
	SnapshotID string          `json:"snapshot_id,omitempty"`
	ObservedAt string          `json:"observed_at,omitempty"`
	Body       json.RawMessage `json:"body"`
}

type dirSyncError struct {
	Err error
}

func (e *dirSyncError) Error() string {
	return "sync do diretório recusado"
}

func (e *dirSyncError) Unwrap() error {
	return e.Err
}

func statePaths(stateDir string) (string, string) {
	return filepath.Join(stateDir, "spool.json"), filepath.Join(stateDir, "latest.json")
}

func loadSpool(path string) (spoolFile, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return spoolFile{Items: []spoolItem{}}, nil
	}
	if err != nil {
		return spoolFile{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var file spoolFile
	if err := decoder.Decode(&file); err != nil {
		return spoolFile{}, err
	}
	if file.Items == nil {
		file.Items = []spoolItem{}
	}
	return file, nil
}

func saveSpool(path string, file spoolFile, dirSync func(string) error) error {
	if file.Items == nil {
		file.Items = []spoolItem{}
	}
	body, err := encodeSpool(file)
	if err != nil {
		return err
	}
	return atomicWrite(path, body, dirSync)
}

func encodeSpool(file spoolFile) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(file); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func baselineDesk(spool spoolFile, latestPath string) (contract.Snapshot, bool, error) {
	for i := len(spool.Items) - 1; i >= 0; i-- {
		item := spool.Items[i]
		if item.Kind != kindSnapshot {
			continue
		}
		snap, err := contract.ValidateSnapshot(item.Body)
		if err != nil {
			return contract.Snapshot{}, false, err
		}
		return snap, true, nil
	}
	raw, err := os.ReadFile(latestPath)
	if errors.Is(err, os.ErrNotExist) {
		return contract.Snapshot{}, false, nil
	}
	if err != nil {
		return contract.Snapshot{}, false, err
	}
	snap, err := contract.ValidateSnapshot(raw)
	if err != nil {
		return contract.Snapshot{}, false, err
	}
	return snap, true, nil
}

func matchingSnapshotBody(spool spoolFile, panes []contract.Pane, layouts []contract.Layout) ([]byte, bool, error) {
	for i := len(spool.Items) - 1; i >= 0; i-- {
		item := spool.Items[i]
		if item.Kind != kindSnapshot {
			continue
		}
		snap, err := contract.ValidateSnapshot(item.Body)
		if err != nil {
			return nil, false, err
		}
		changed, err := deskChanged(snap.Panes, snap.Layouts, panes, layouts)
		if err != nil {
			return nil, false, err
		}
		if !changed {
			return append([]byte{}, item.Body...), true, nil
		}
		return nil, false, nil
	}
	return nil, false, nil
}

func atomicWrite(path string, data []byte, dirSync func(string) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".herdr-recall-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	if err := dirSync(dir); err != nil {
		return &dirSyncError{Err: err}
	}
	return nil
}

func syncDir(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func writeDirSyncWarn(stateDir string, now time.Time) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(stateDir, "dirsync-warn")
	line := contract.FormatTime(now.UTC()) + " Sync do diretório foi recusado\n"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(line); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
