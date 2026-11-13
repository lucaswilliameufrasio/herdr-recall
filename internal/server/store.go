package server

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"herdr-recall/internal/contract"

	_ "modernc.org/sqlite"
)

const (
	schemaVersion = 1
	maxBoots      = 14
	maxReceipts   = 50
	presenceKeep  = 15 * time.Minute

	StatusOK             = 200
	StatusConflict       = 409
	StatusUnprocessable  = 422
	CodeSnapshotConflict = "SNAPSHOT_CONFLICT"
)

// Outcome is the ingest decision. It is not an HTTP server.
type Outcome struct {
	Status int
	Code   string
}

// Store is the SQLite file for one installation.
type Store struct {
	db *sql.DB
}

// Open creates the schema in an empty file. It does not listen.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("abrir sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.useWAL(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateDevice inserts one machine. Enrollment checks the name later.
func (s *Store) CreateDevice(id, name, tokenHash string, now time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO devices (id, name, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		id, name, tokenHash, stamp(now),
	)
	if err != nil {
		return fmt.Errorf("criar máquina: %w", err)
	}
	return nil
}

// IngestSnapshot applies the desk rules. The stored payload is canonical JSON.
func (s *Store) IngestSnapshot(deviceID string, raw []byte, now time.Time) (Outcome, error) {
	snap, err := contract.ValidateSnapshot(raw)
	if err != nil {
		return Outcome{Status: StatusUnprocessable}, nil
	}
	payload, err := contract.MarshalSnapshot(snap)
	if err != nil {
		return Outcome{}, err
	}
	hash, err := contract.SemanticHash(snap)
	if err != nil {
		return Outcome{}, err
	}
	return s.ingest(deviceID, now, func(tx *sql.Tx, received string) (Outcome, bool, error) {
		return applySnapshot(tx, deviceID, snap, payload, hash, received)
	})
}

// IngestPresence records a check-in. A repeated observed_at is still a success.
func (s *Store) IngestPresence(deviceID string, raw []byte, now time.Time) (Outcome, error) {
	presence, err := contract.ValidatePresence(raw)
	if err != nil {
		return Outcome{Status: StatusUnprocessable}, nil
	}
	return s.ingest(deviceID, now, func(tx *sql.Tx, received string) (Outcome, bool, error) {
		return applyPresence(tx, deviceID, presence, received)
	})
}

func (s *Store) ingest(deviceID string, now time.Time, apply func(tx *sql.Tx, received string) (Outcome, bool, error)) (Outcome, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Outcome{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := requireDevice(tx, deviceID); err != nil {
		return Outcome{}, err
	}
	outcome, wrote, err := apply(tx, stamp(now))
	if err != nil {
		return Outcome{}, err
	}
	if wrote {
		if err := retain(tx, deviceID, now); err != nil {
			return Outcome{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Outcome{}, err
	}
	return outcome, nil
}

func applySnapshot(tx *sql.Tx, deviceID string, snap contract.Snapshot, payload []byte, hash, received string) (Outcome, bool, error) {
	var storedHash string
	err := tx.QueryRow(
		`SELECT semantic_hash FROM snapshot_receipts WHERE device_id = ? AND snapshot_id = ?`,
		deviceID, snap.SnapshotID,
	).Scan(&storedHash)
	if err == nil {
		if storedHash == hash {
			return Outcome{Status: StatusOK}, false, nil
		}
		return Outcome{Status: StatusConflict, Code: CodeSnapshotConflict}, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, false, err
	}

	var currentCaptured string
	err = tx.QueryRow(
		`SELECT captured_at FROM snapshots WHERE device_id = ? AND boot_id = ?`,
		deviceID, snap.BootID,
	).Scan(&currentCaptured)
	if errors.Is(err, sql.ErrNoRows) {
		if err := insertBoot(tx, deviceID, snap.BootID, received); err != nil {
			return Outcome{}, false, err
		}
		if _, err := tx.Exec(
			`INSERT INTO snapshots (device_id, boot_id, snapshot_id, captured_at, received_at, semantic_hash, payload)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			deviceID, snap.BootID, snap.SnapshotID, snap.CapturedAt, received, hash, string(payload),
		); err != nil {
			return Outcome{}, false, err
		}
		if err := insertReceipt(tx, deviceID, snap.SnapshotID, hash, received); err != nil {
			return Outcome{}, false, err
		}
		return Outcome{Status: StatusOK}, true, nil
	}
	if err != nil {
		return Outcome{}, false, err
	}
	if snap.CapturedAt > currentCaptured {
		if _, err := tx.Exec(
			`UPDATE snapshots
			 SET snapshot_id = ?, captured_at = ?, received_at = ?, semantic_hash = ?, payload = ?
			 WHERE device_id = ? AND boot_id = ?`,
			snap.SnapshotID, snap.CapturedAt, received, hash, string(payload), deviceID, snap.BootID,
		); err != nil {
			return Outcome{}, false, err
		}
		if err := insertReceipt(tx, deviceID, snap.SnapshotID, hash, received); err != nil {
			return Outcome{}, false, err
		}
		return Outcome{Status: StatusOK}, true, nil
	}
	if err := insertReceipt(tx, deviceID, snap.SnapshotID, hash, received); err != nil {
		return Outcome{}, false, err
	}
	return Outcome{Status: StatusOK}, true, nil
}

func applyPresence(tx *sql.Tx, deviceID string, presence contract.Presence, received string) (Outcome, bool, error) {
	herdrOK := 0
	if presence.HerdrOK {
		herdrOK = 1
	}
	result, err := tx.Exec(
		`INSERT INTO presence (device_id, observed_at, received_at, boot_id, pane_count, herdr_ok)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (device_id, observed_at) DO NOTHING`,
		deviceID, presence.ObservedAt, received, presence.BootID, presence.PaneCount, herdrOK,
	)
	if err != nil {
		return Outcome{}, false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return Outcome{}, false, err
	}
	if inserted == 0 {
		return Outcome{Status: StatusOK}, true, nil
	}
	if err := insertBoot(tx, deviceID, presence.BootID, received); err != nil {
		return Outcome{}, false, err
	}
	if _, err := tx.Exec(
		`UPDATE devices SET last_seen_at = ? WHERE id = ? AND (last_seen_at IS NULL OR last_seen_at < ?)`,
		received, deviceID, received,
	); err != nil {
		return Outcome{}, false, err
	}
	return Outcome{Status: StatusOK}, true, nil
}

func insertBoot(tx *sql.Tx, deviceID, bootID, received string) error {
	_, err := tx.Exec(
		`INSERT INTO boots (device_id, boot_id, first_received_at) VALUES (?, ?, ?)
		 ON CONFLICT (device_id, boot_id) DO NOTHING`,
		deviceID, bootID, received,
	)
	return err
}

func insertReceipt(tx *sql.Tx, deviceID, snapshotID, hash, received string) error {
	_, err := tx.Exec(
		`INSERT INTO snapshot_receipts (device_id, snapshot_id, semantic_hash, received_at) VALUES (?, ?, ?, ?)`,
		deviceID, snapshotID, hash, received,
	)
	return err
}

func requireDevice(tx *sql.Tx, deviceID string) error {
	var one int
	err := tx.QueryRow(`SELECT 1 FROM devices WHERE id = ?`, deviceID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("máquina desconhecida")
	}
	return err
}

// retain caps boots, receipts, and presence using the server clock.
// Boot order is first_received_at. Presence age is received_at, not observed_at.
func retain(tx *sql.Tx, deviceID string, now time.Time) error {
	if err := evictBoots(tx, deviceID); err != nil {
		return err
	}
	if err := capReceipts(tx, deviceID); err != nil {
		return err
	}
	cutoff := stamp(now.UTC().Add(-presenceKeep))
	_, err := tx.Exec(
		`DELETE FROM presence WHERE device_id = ? AND received_at < ?`,
		deviceID, cutoff,
	)
	return err
}

func evictBoots(tx *sql.Tx, deviceID string) error {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM boots WHERE device_id = ?`, deviceID).Scan(&count); err != nil {
		return err
	}
	extra := count - maxBoots
	if extra <= 0 {
		return nil
	}
	rows, err := tx.Query(
		`SELECT boot_id FROM boots WHERE device_id = ?
		 ORDER BY first_received_at ASC, boot_id ASC LIMIT ?`,
		deviceID, extra,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	ids := make([]string, 0, extra)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return deleteIDs(tx, deviceID, ids, []string{
		`DELETE FROM snapshots WHERE device_id = ? AND boot_id IN (%s)`,
		`DELETE FROM boots WHERE device_id = ? AND boot_id IN (%s)`,
	})
}

func capReceipts(tx *sql.Tx, deviceID string) error {
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM snapshot_receipts WHERE device_id = ?`, deviceID).Scan(&count); err != nil {
		return err
	}
	extra := count - maxReceipts
	if extra <= 0 {
		return nil
	}
	rows, err := tx.Query(
		`SELECT snapshot_id FROM snapshot_receipts WHERE device_id = ?
		 ORDER BY received_at ASC, snapshot_id ASC LIMIT ?`,
		deviceID, extra,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	ids := make([]string, 0, extra)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return deleteIDs(tx, deviceID, ids, []string{
		`DELETE FROM snapshot_receipts WHERE device_id = ? AND snapshot_id IN (%s)`,
	})
}

func deleteIDs(tx *sql.Tx, deviceID string, ids []string, statements []string) error {
	if len(ids) == 0 {
		return nil
	}
	marks := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, deviceID)
	for i, id := range ids {
		marks[i] = "?"
		args = append(args, id)
	}
	list := strings.Join(marks, ",")
	for _, statement := range statements {
		if _, err := tx.Exec(fmt.Sprintf(statement, list), args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) useWAL() error {
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode); err != nil {
		return fmt.Errorf("abrir sqlite em wal: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("abrir sqlite em wal: modo %s", mode)
	}
	return nil
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("ler versão: %w", err)
	}
	if version == schemaVersion {
		return nil
	}
	if version != 0 {
		return fmt.Errorf("sqlite user_version %d não é suportada", version)
	}
	if _, err := s.db.Exec(schemaV1); err != nil {
		return fmt.Errorf("criar esquema: %w", err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 1`); err != nil {
		return fmt.Errorf("gravar versão: %w", err)
	}
	return nil
}

func stamp(now time.Time) string {
	return contract.FormatTime(now.UTC())
}

const schemaV1 = `
CREATE TABLE operator (
  id            INTEGER PRIMARY KEY CHECK (id = 1),
  password_hash TEXT NOT NULL,
  created_at    TEXT NOT NULL
);

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  csrf       TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);

CREATE TABLE enrollment_codes (
  code_hash  TEXT PRIMARY KEY,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at    TEXT
);

CREATE TABLE boots (
  device_id         TEXT NOT NULL,
  boot_id           TEXT NOT NULL,
  first_received_at TEXT NOT NULL,
  PRIMARY KEY (device_id, boot_id)
);

CREATE TABLE devices (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  token_hash   TEXT NOT NULL UNIQUE,
  created_at   TEXT NOT NULL,
  revoked_at   TEXT,
  last_seen_at TEXT
);

CREATE TABLE snapshots (
  device_id      TEXT NOT NULL,
  boot_id        TEXT NOT NULL,
  snapshot_id    TEXT NOT NULL,
  captured_at    TEXT NOT NULL,
  received_at    TEXT NOT NULL,
  semantic_hash  TEXT NOT NULL,
  payload        TEXT NOT NULL,
  PRIMARY KEY (device_id, boot_id)
);

CREATE TABLE snapshot_receipts (
  device_id     TEXT NOT NULL,
  snapshot_id   TEXT NOT NULL,
  semantic_hash TEXT NOT NULL,
  received_at   TEXT NOT NULL,
  PRIMARY KEY (device_id, snapshot_id)
);

CREATE TABLE presence (
  device_id   TEXT NOT NULL,
  observed_at TEXT NOT NULL,
  received_at TEXT NOT NULL,
  boot_id     TEXT NOT NULL,
  pane_count  INTEGER NOT NULL,
  herdr_ok    INTEGER NOT NULL,
  PRIMARY KEY (device_id, observed_at)
);
`
