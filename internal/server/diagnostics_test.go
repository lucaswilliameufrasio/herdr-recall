package server

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticsAuthorizationOrderingAndFreshness(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	h.setupOperator(t)
	cookie, csrf := h.login(t)
	code, _ := h.issueCode(t, cookie, csrf)
	id, token, _ := h.enroll(t, code, "Mesa")
	body := []byte(`{"observed_at":"2026-11-13T00:09:00.000000000Z","pending_count":3,"conflict_count":1}`)
	denied := h.call(http.MethodPost, "/v1/diagnostics", body, jsonHeader(cookieHeader(cookie)))
	mustCode(t, denied, statusUnauthorized, codeUnauthorized)
	report := h.call(http.MethodPost, "/v1/diagnostics", body, jsonHeader(bearerHeader(token)))
	if report.Code != http.StatusNoContent {
		t.Fatalf("relato: %d", report.Code)
	}
	older := []byte(`{"observed_at":"2026-11-13T00:08:00.000000000Z","pending_count":0,"conflict_count":0}`)
	outdated := h.call(http.MethodPost, "/v1/diagnostics", older, jsonHeader(bearerHeader(token)))
	mustCode(t, outdated, http.StatusConflict, "DIAGNOSTIC_OUTDATED")
	page := h.call(http.MethodGet, "/maquinas/"+id, nil, cookieHeader(cookie))
	if !strings.Contains(page.Body.String(), "Recente: 3 itens pendentes, 1 em quarentena") {
		t.Fatal("contagens reais recentes ausentes")
	}
	h.clock.at = h.clock.at.Add(5 * time.Minute)
	page = h.call(http.MethodGet, "/maquinas/"+id, nil, cookieHeader(cookie))
	if !strings.Contains(page.Body.String(), "Antigo ou relógio divergente: 3 itens pendentes") {
		t.Fatal("relato antigo apresentado como atual")
	}
	invalid := []byte(`{"observed_at":"2026-11-13T00:09:00.000000000Z","pending_count":-1,"conflict_count":0}`)
	mustCode(t, h.call(http.MethodPost, "/v1/diagnostics", invalid, jsonHeader(bearerHeader(token))), StatusUnprocessable, codeInvalidParams)
}

func TestMigrationV1PreservesExistingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaV1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO devices (id,name,token_hash,created_at) VALUES ('old','Mesa','hash','2026-11-13T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if count(t, store, `SELECT COUNT(*) FROM devices WHERE id='old'`) != 1 {
		t.Fatal("migração perdeu máquina")
	}
	var version int
	if err := store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 2 {
		t.Fatal("versão não migrada")
	}
}
