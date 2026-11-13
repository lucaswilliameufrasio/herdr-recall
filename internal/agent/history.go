package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"herdr-recall/internal/contract"
)

var (
	historyMaxBytes int64 = 64 * 1024 * 1024
	historyKeep           = 48 * time.Hour
)

func appendHistory(stateDir string, lineTime, now time.Time, snapshot []byte, dirSync func(string) error) error {
	day := lineTime.UTC().Format("20060102")
	dir := filepath.Join(stateDir, "history")
	path := filepath.Join(dir, day+".jsonl")
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var buf bytes.Buffer
	if len(bytes.TrimSpace(existing)) > 0 {
		buf.Write(existing)
		if !bytes.HasSuffix(existing, []byte("\n")) {
			buf.WriteByte('\n')
		}
	}
	buf.Write(snapshot)
	buf.WriteByte('\n')
	if err := atomicWrite(path, buf.Bytes(), dirSync); err != nil {
		return err
	}
	return pruneHistory(dir, now, dirSync)
}

func historyTime(body []byte, fallback time.Time) time.Time {
	var probe struct {
		CapturedAt string `json:"captured_at"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return fallback
	}
	parsed, ok := contract.ParseTime(probe.CapturedAt)
	if !ok {
		return fallback
	}
	return parsed
}

func pruneHistory(dir string, now time.Time, dirSync func(string) error) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !historyName(entry.Name()) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	cutoff := now.UTC().Add(-historyKeep)
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := dropOldLines(path, cutoff, dirSync); err != nil {
			return err
		}
	}
	names, err = historyFiles(dir)
	if err != nil {
		return err
	}
	for len(names) > 0 && historySize(dir, names) > historyMaxBytes {
		oldest := filepath.Join(dir, names[0])
		if len(names) == 1 {
			if err := trimOldestLines(oldest, dirSync); err != nil {
				return err
			}
			return dirSync(dir)
		}
		if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		names = names[1:]
	}
	return dirSync(dir)
}

func dropOldLines(path string, cutoff time.Time, dirSync func(string) error) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	kept := make([][]byte, 0)
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		when, ok := lineTime(line)
		if !ok || when.Before(cutoff) {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	body := append(bytes.Join(kept, []byte("\n")), '\n')
	if bytes.Equal(body, ensureTrailingNL(raw)) {
		return nil
	}
	return atomicWrite(path, body, dirSync)
}

func trimOldestLines(path string, dirSync func(string) error) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := splitLines(raw)
	for len(lines) > 0 && int64(lineSize(lines)) > historyMaxBytes {
		lines = lines[1:]
	}
	if len(lines) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	body := append(bytes.Join(lines, []byte("\n")), '\n')
	return atomicWrite(path, body, dirSync)
}

func lineTime(line []byte) (time.Time, bool) {
	var probe struct {
		CapturedAt string `json:"captured_at"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return time.Time{}, false
	}
	return contract.ParseTime(probe.CapturedAt)
}

func historyFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !historyName(entry.Name()) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func historyName(name string) bool {
	if len(name) != len("20060102.jsonl") || !strings.HasSuffix(name, ".jsonl") {
		return false
	}
	for _, r := range name[:8] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func historySize(dir string, names []string) int64 {
	var total int64
	for _, name := range names {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total
}

func splitLines(raw []byte) [][]byte {
	parts := bytes.Split(raw, []byte("\n"))
	lines := make([][]byte, 0, len(parts))
	for _, part := range parts {
		if len(bytes.TrimSpace(part)) == 0 {
			continue
		}
		lines = append(lines, part)
	}
	return lines
}

func lineSize(lines [][]byte) int {
	size := 0
	for _, line := range lines {
		size += len(line) + 1
	}
	return size
}

func ensureTrailingNL(raw []byte) []byte {
	if len(raw) == 0 || raw[len(raw)-1] == '\n' {
		return raw
	}
	return append(append([]byte{}, raw...), '\n')
}
