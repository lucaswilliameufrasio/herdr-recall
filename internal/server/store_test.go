package server

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"herdr-recall/internal/contract"

	_ "modernc.org/sqlite"
)

func TestOpenCreatesSchemaVersion1(t *testing.T) {
	store := openStore(t)
	var version int
	if err := store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("versão: %d", version)
	}
	var mode string
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("modo: %s", mode)
	}
	for _, table := range []string{"operator", "sessions", "enrollment_codes", "boots", "devices", "snapshots", "snapshot_receipts", "presence"} {
		var name string
		err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("tabela %s: %v", table, err)
		}
	}
}

func TestOpenRejectsANewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recall.db")
	store := openAt(t, path)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("versão nova foi aceita")
	}
}

func TestIngestRejectsInvalidBody(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	outcome, err := store.IngestSnapshot("dev-1", []byte(`{"snapshot_id":"0123456789abcdef","scroll":[]}`), at(1))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != StatusUnprocessable {
		t.Fatalf("status: %d", outcome.Status)
	}
	if count(t, store, `SELECT COUNT(*) FROM snapshots`) != 0 || count(t, store, `SELECT COUNT(*) FROM snapshot_receipts`) != 0 {
		t.Fatal("corpo inválido foi escrito")
	}
	outcome, err = store.IngestPresence("dev-1", []byte(`{"boot_id":"boot-1"}`), at(1))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != StatusUnprocessable || count(t, store, `SELECT COUNT(*) FROM presence`) != 0 {
		t.Fatalf("presença: %d", outcome.Status)
	}
}

func TestIdempotentSnapshotDoesNotMoveClocks(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	body := desk("0123456789abcdef", "boot-1", stampAt(10))
	first, err := store.IngestSnapshot("dev-1", body, at(1))
	if err != nil || first.Status != StatusOK {
		t.Fatalf("primeira: %+v %v", first, err)
	}
	spaced := []byte("{\n  \"snapshot_id\": \"0123456789abcdef\",\n  \"host\": \"desk\",\n  \"boot_id\": \"boot-1\",\n  \"captured_at\": \"" + stampAt(10) + "\",\n  \"panes\": []\n}\n")
	second, err := store.IngestSnapshot("dev-1", spaced, at(5))
	if err != nil || second.Status != StatusOK || second.Code != "" {
		t.Fatalf("retry: %+v %v", second, err)
	}
	row := readDesk(t, store, "dev-1", "boot-1")
	if row.received != stampAt(1) || row.snapshotID != "0123456789abcdef" {
		t.Fatalf("mesa: %+v", row)
	}
	if bootTime(t, store, "dev-1", "boot-1") != stampAt(1) {
		t.Fatal("first_received_at andou")
	}
	if receiptTime(t, store, "dev-1", "0123456789abcdef") != stampAt(1) {
		t.Fatal("recibo andou")
	}
	if row.payload != string(body) {
		t.Fatalf("payload: %s", row.payload)
	}
}

func TestOlderSnapshotDoesNotCoverTheDesk(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	newer := desk("0123456789abcdef", "boot-1", stampAt(20))
	older := desk("fedcba9876543210", "boot-1", stampAt(10))
	if _, err := store.IngestSnapshot("dev-1", newer, at(2)); err != nil {
		t.Fatal(err)
	}
	outcome, err := store.IngestSnapshot("dev-1", older, at(3))
	if err != nil || outcome.Status != StatusOK {
		t.Fatalf("id velho: %+v %v", outcome, err)
	}
	row := readDesk(t, store, "dev-1", "boot-1")
	if row.snapshotID != "0123456789abcdef" || row.received != stampAt(2) || row.captured != stampAt(20) {
		t.Fatalf("mesa coberta: %+v", row)
	}
	if bootTime(t, store, "dev-1", "boot-1") != stampAt(2) {
		t.Fatal("boot andou")
	}
	sameTime := desk("abcdef0123456789", "boot-1", stampAt(20))
	outcome, err = store.IngestSnapshot("dev-1", sameTime, at(4))
	if err != nil || outcome.Status != StatusOK {
		t.Fatalf("hora igual: %+v %v", outcome, err)
	}
	row = readDesk(t, store, "dev-1", "boot-1")
	if row.snapshotID != "0123456789abcdef" || row.received != stampAt(2) {
		t.Fatalf("hora igual cobriu: %+v", row)
	}
	changed := deskChanged("fedcba9876543210", "boot-1", stampAt(10))
	outcome, err = store.IngestSnapshot("dev-1", changed, at(5))
	if err != nil || outcome.Status != StatusConflict || outcome.Code != CodeSnapshotConflict {
		t.Fatalf("conflito do id velho: %+v %v", outcome, err)
	}
	row = readDesk(t, store, "dev-1", "boot-1")
	if row.snapshotID != "0123456789abcdef" {
		t.Fatalf("409 cobriu: %+v", row)
	}
}

func TestHashMismatchIsConflict(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	first := desk("0123456789abcdef", "boot-1", stampAt(10))
	if _, err := store.IngestSnapshot("dev-1", first, at(1)); err != nil {
		t.Fatal(err)
	}
	second := desk("0123456789abcdef", "boot-1", stampAt(30))
	outcome, err := store.IngestSnapshot("dev-1", second, at(2))
	if err != nil || outcome.Status != StatusConflict || outcome.Code != CodeSnapshotConflict {
		t.Fatalf("409: %+v %v", outcome, err)
	}
	row := readDesk(t, store, "dev-1", "boot-1")
	if row.snapshotID != "0123456789abcdef" || row.captured != stampAt(10) || row.received != stampAt(1) {
		t.Fatalf("mesa: %+v", row)
	}
	if receiptTime(t, store, "dev-1", "0123456789abcdef") != stampAt(1) {
		t.Fatal("recibo do conflito andou")
	}
}

func TestNewerSnapshotReplacesTheDesk(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	if _, err := store.IngestSnapshot("dev-1", desk("0123456789abcdef", "boot-1", stampAt(10)), at(1)); err != nil {
		t.Fatal(err)
	}
	outcome, err := store.IngestSnapshot("dev-1", desk("fedcba9876543210", "boot-1", stampAt(11)), at(4))
	if err != nil || outcome.Status != StatusOK {
		t.Fatalf("nova: %+v %v", outcome, err)
	}
	row := readDesk(t, store, "dev-1", "boot-1")
	if row.snapshotID != "fedcba9876543210" || row.captured != stampAt(11) || row.received != stampAt(4) {
		t.Fatalf("mesa: %+v", row)
	}
	if bootTime(t, store, "dev-1", "boot-1") != stampAt(1) {
		t.Fatal("first_received_at andou na troca")
	}
}

func TestPresenceConflictKeepsTheFirstRow(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	observed := stampAt(1)
	first := presenceBody("boot-1", observed, 4, false)
	second := presenceBody("boot-1", observed, 9, true)
	outcome, err := store.IngestPresence("dev-1", first, at(2))
	if err != nil || outcome.Status != StatusOK {
		t.Fatalf("primeira: %+v %v", outcome, err)
	}
	outcome, err = store.IngestPresence("dev-1", second, at(2))
	if err != nil || outcome.Status != StatusOK || outcome.Code != "" {
		t.Fatalf("repetida: %+v %v", outcome, err)
	}
	var herdrOK, panes int
	var received string
	if err := store.db.QueryRow(
		`SELECT herdr_ok, pane_count, received_at FROM presence WHERE device_id = ? AND observed_at = ?`,
		"dev-1", observed,
	).Scan(&herdrOK, &panes, &received); err != nil {
		t.Fatal(err)
	}
	if herdrOK != 0 || panes != 4 || received != stampAt(2) {
		t.Fatalf("linha: ok=%d panes=%d received=%s", herdrOK, panes, received)
	}
	if lastSeen(t, store, "dev-1") != stampAt(2) {
		t.Fatal("last_seen andou no replay")
	}
	if bootTime(t, store, "dev-1", "boot-1") != stampAt(2) {
		t.Fatal("boot andou no replay")
	}
}

func TestFreshnessFollowsPresenceReceivedAt(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	if _, err := store.IngestSnapshot("dev-1", desk("0123456789abcdef", "boot-1", stampAt(10)), at(3)); err != nil {
		t.Fatal(err)
	}
	if seen := lastSeen(t, store, "dev-1"); seen != "" {
		t.Fatalf("snapshot moveu last_seen: %s", seen)
	}
	lateObserved := contract.FormatTime(at(0).Add(-5 * time.Hour))
	outcome, err := store.IngestPresence("dev-1", presenceBody("boot-1", lateObserved, 2, true), at(3))
	if err != nil || outcome.Status != StatusOK {
		t.Fatalf("presença: %+v %v", outcome, err)
	}
	if lastSeen(t, store, "dev-1") != stampAt(3) {
		t.Fatal("last_seen não é o relógio do servidor")
	}
	outcome, err = store.IngestPresence("dev-1", presenceBody("boot-2", stampAt(1), 1, true), at(2))
	if err != nil || outcome.Status != StatusOK {
		t.Fatal(err)
	}
	if lastSeen(t, store, "dev-1") != stampAt(3) {
		t.Fatal("last_seen recuou")
	}
	if _, err := store.IngestSnapshot("dev-1", desk("fedcba9876543210", "boot-1", stampAt(40)), at(6)); err != nil {
		t.Fatal(err)
	}
	if lastSeen(t, store, "dev-1") != stampAt(3) {
		t.Fatal("snapshot posterior moveu last_seen")
	}
}

func TestPresencePruneUsesReceivedAt(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	oldObserved := contract.FormatTime(at(0).Add(-5 * time.Hour))
	if _, err := store.IngestPresence("dev-1", presenceBody("boot-1", oldObserved, 3, false), at(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.IngestPresence("dev-1", presenceBody("boot-1", stampAt(1), 3, true), at(20)); err != nil {
		t.Fatal(err)
	}
	if count(t, store, `SELECT COUNT(*) FROM presence WHERE observed_at = '`+oldObserved+`'`) != 0 {
		t.Fatal("presença velha no relógio do servidor ficou")
	}
	var received string
	if err := store.db.QueryRow(`SELECT received_at FROM presence WHERE observed_at = ?`, stampAt(1)).Scan(&received); err != nil {
		t.Fatal(err)
	}
	if received != stampAt(20) {
		t.Fatalf("linha nova: %s", received)
	}
	if lastSeen(t, store, "dev-1") != stampAt(20) {
		t.Fatal("poda apagou last_seen")
	}
}

func TestEvictsOldestBoot(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	mustDevice(t, store, "dev-2", at(0))
	if _, err := store.IngestSnapshot("dev-2", desk("bbbbbbbbbbbbbbbb", "boot-keep", stampAt(1)), at(0)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 15; i++ {
		id := snapID(i)
		boot := bootID(i)
		outcome, err := store.IngestSnapshot("dev-1", desk(id, boot, stampAt(i)), at(i))
		if err != nil || outcome.Status != StatusOK {
			t.Fatalf("boot %d: %+v %v", i, outcome, err)
		}
	}
	if count(t, store, `SELECT COUNT(*) FROM boots WHERE device_id = 'dev-1'`) != 14 {
		t.Fatal("não ficou em 14 boots")
	}
	if count(t, store, `SELECT COUNT(*) FROM snapshots WHERE device_id = 'dev-1' AND boot_id = 'boot-00'`) != 0 {
		t.Fatal("o boot mais antigo ficou")
	}
	if count(t, store, `SELECT COUNT(*) FROM snapshots WHERE device_id = 'dev-1' AND boot_id = 'boot-14'`) != 1 {
		t.Fatal("o boot novo saiu")
	}
	if bootTime(t, store, "dev-2", "boot-keep") != stampAt(0) {
		t.Fatal("a outra máquina perdeu o boot")
	}
}

func TestBootTieBreaksByBootID(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	for i, boot := range []string{"boot-b", "boot-a"} {
		if _, err := store.IngestSnapshot("dev-1", desk(snapID(i), boot, stampAt(1)), at(1)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 13; i++ {
		if _, err := store.IngestSnapshot("dev-1", desk(snapID(i+10), bootID(i+10), stampAt(i+10)), at(i+2)); err != nil {
			t.Fatal(err)
		}
	}
	if count(t, store, `SELECT COUNT(*) FROM boots WHERE device_id = 'dev-1'`) != 14 {
		t.Fatal("contagem")
	}
	if count(t, store, `SELECT COUNT(*) FROM snapshots WHERE device_id = 'dev-1' AND boot_id = 'boot-a'`) != 0 {
		t.Fatal("o menor boot_id empatado ficou")
	}
	if count(t, store, `SELECT COUNT(*) FROM snapshots WHERE device_id = 'dev-1' AND boot_id = 'boot-b'`) != 1 {
		t.Fatal("o outro boot empatado saiu")
	}
}

func TestReceiptCapKeepsReplayFromCoveringTheDesk(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	var first []byte
	for i := 0; i < 51; i++ {
		body := desk(snapID(i), "boot-1", stampAt(i))
		if i == 0 {
			first = body
		}
		outcome, err := store.IngestSnapshot("dev-1", body, at(i))
		if err != nil || outcome.Status != StatusOK {
			t.Fatalf("%d: %+v %v", i, outcome, err)
		}
	}
	if count(t, store, `SELECT COUNT(*) FROM snapshot_receipts WHERE device_id = 'dev-1'`) != 50 {
		t.Fatal("recibos passaram de 50")
	}
	if count(t, store, `SELECT COUNT(*) FROM snapshot_receipts WHERE snapshot_id = '`+snapID(0)+`'`) != 0 {
		t.Fatal("o recibo mais antigo ficou")
	}
	row := readDesk(t, store, "dev-1", "boot-1")
	outcome, err := store.IngestSnapshot("dev-1", first, at(60))
	if err != nil || outcome.Status != StatusOK {
		t.Fatalf("replay: %+v %v", outcome, err)
	}
	again := readDesk(t, store, "dev-1", "boot-1")
	if again != row {
		t.Fatalf("replay cobriu a mesa: %+v", again)
	}
	if bootTime(t, store, "dev-1", "boot-1") != stampAt(0) {
		t.Fatal("replay moveu o boot")
	}
}

type deskRow struct {
	snapshotID string
	captured   string
	received   string
	payload    string
}

func openStore(t *testing.T) *Store {
	t.Helper()
	return openAt(t, filepath.Join(t.TempDir(), "recall.db"))
}

func openAt(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func mustDevice(t *testing.T, store *Store, id string, now time.Time) {
	t.Helper()
	if err := store.CreateDevice(id, "mesa", "hash-"+id, now); err != nil {
		t.Fatal(err)
	}
}

func desk(id, boot, captured string) []byte {
	raw, err := contract.MarshalSnapshot(contract.Snapshot{
		SnapshotID: id,
		Host:       "desk",
		BootID:     boot,
		CapturedAt: captured,
		Panes:      []contract.Pane{},
	})
	if err != nil {
		panic(err)
	}
	return raw
}

func deskChanged(id, boot, captured string) []byte {
	raw, err := contract.MarshalSnapshot(contract.Snapshot{
		SnapshotID: id,
		Host:       "other",
		BootID:     boot,
		CapturedAt: captured,
		Panes:      []contract.Pane{},
	})
	if err != nil {
		panic(err)
	}
	return raw
}

func presenceBody(boot, observed string, panes int, herdrOK bool) []byte {
	raw, err := contract.MarshalPresence(contract.Presence{
		BootID:     boot,
		ObservedAt: observed,
		PaneCount:  panes,
		HerdrOK:    herdrOK,
	})
	if err != nil {
		panic(err)
	}
	return raw
}

func at(minute int) time.Time {
	return time.Date(2026, 11, 13, 0, minute, 0, 0, time.UTC)
}

func stampAt(minute int) string {
	return contract.FormatTime(at(minute))
}

func snapID(n int) string {
	return fmt.Sprintf("id%014d", n)
}

func bootID(n int) string {
	return "boot-" + two(n)
}

func two(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func count(t *testing.T, store *Store, query string) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func readDesk(t *testing.T, store *Store, deviceID, bootID string) deskRow {
	t.Helper()
	var row deskRow
	err := store.db.QueryRow(
		`SELECT snapshot_id, captured_at, received_at, payload FROM snapshots WHERE device_id = ? AND boot_id = ?`,
		deviceID, bootID,
	).Scan(&row.snapshotID, &row.captured, &row.received, &row.payload)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func bootTime(t *testing.T, store *Store, deviceID, bootID string) string {
	t.Helper()
	var value string
	if err := store.db.QueryRow(
		`SELECT first_received_at FROM boots WHERE device_id = ? AND boot_id = ?`,
		deviceID, bootID,
	).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func receiptTime(t *testing.T, store *Store, deviceID, snapshotID string) string {
	t.Helper()
	var value string
	if err := store.db.QueryRow(
		`SELECT received_at FROM snapshot_receipts WHERE device_id = ? AND snapshot_id = ?`,
		deviceID, snapshotID,
	).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func lastSeen(t *testing.T, store *Store, deviceID string) string {
	t.Helper()
	var value sql.NullString
	if err := store.db.QueryRow(`SELECT last_seen_at FROM devices WHERE id = ?`, deviceID).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if !value.Valid {
		return ""
	}
	return value.String
}
