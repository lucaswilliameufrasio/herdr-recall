package server

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"
	"time"

	"herdr-recall/internal/web"
)

func TestPanelStatePrecedence(t *testing.T) {
	now := at(10)
	recent := now.Add(-time.Minute)
	old := now.Add(-4 * time.Minute)
	for _, tc := range []struct {
		name      string
		lastSeen  sql.NullString
		snapshot  bool
		previous  bool
		newHas    bool
		present   bool
		presentAt time.Time
		herdrOK   bool
		captured  time.Time
		want      string
	}{
		{"sem presença", sql.NullString{}, false, false, false, false, time.Time{}, false, time.Time{}, "Sem presença nesta máquina."},
		{"sem sinal vence boot e herdr", sql.NullString{String: stamp(old), Valid: true}, true, true, false, true, old, false, old, "Sem sinal desde"},
		{"boot anterior aguardando", sql.NullString{String: stamp(recent), Valid: true}, true, true, false, true, recent, false, old, "Boot anterior. A mesa nova ainda não chegou."},
		{"boot anterior consultado", sql.NullString{String: stamp(recent), Valid: true}, true, true, true, true, recent, false, old, "Boot anterior."},
		{"herdr sem captura", sql.NullString{String: stamp(recent), Valid: true}, false, false, false, true, recent, false, time.Time{}, "Herdr sem resposta."},
		{"herdr sem resposta", sql.NullString{String: stamp(recent), Valid: true}, true, false, true, true, recent, false, old, "Herdr sem resposta."},
		{"estável", sql.NullString{String: stamp(recent), Valid: true}, true, false, true, true, recent, true, old, "Mesa estável desde"},
		{"vista", sql.NullString{String: stamp(recent), Valid: true}, true, false, true, true, recent, true, recent, "Vista há"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := panelState(now, tc.lastSeen, tc.snapshot, tc.previous, tc.newHas, tc.present, tc.presentAt, tc.herdrOK, tc.captured)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("estado %q, esperado %q", got, tc.want)
			}
		})
	}
}

func TestPanelBootFallbackRevocationAndPresence(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	if result, err := store.IngestSnapshot("dev-1", desk(snapID(1), "old-boot", stampAt(1)), at(1)); err != nil || result.Status != StatusOK {
		t.Fatalf("snapshot: %+v %v", result, err)
	}
	if result, err := store.IngestPresence("dev-1", presenceBody("new-boot", stampAt(2), 0, false), at(3)); err != nil || result.Status != StatusOK {
		t.Fatalf("presence: %+v %v", result, err)
	}
	if found, err := store.RevokeDevice("dev-1", at(4)); err != nil || !found {
		t.Fatalf("revocation: %t %v", found, err)
	}
	detail, found, err := store.panelDetail("dev-1", "", at(4))
	if err != nil || !found || !detail.Revoked || detail.Boot != "old-boot" || detail.Freshness != "Boot anterior. A mesa nova ainda não chegou." {
		t.Fatalf("detail: %+v, %t, %v", detail, found, err)
	}
	var html bytes.Buffer
	if err := web.RenderDetailPage(&html, "csrf", detail); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Agente revogado. A última captura permanece.", "Boot anterior. A mesa nova ainda não chegou.", "Nenhum pane nesta captura."} {
		if !strings.Contains(html.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	detail, found, err = store.panelDetail("dev-1", "", at(8))
	if err != nil || !found || !strings.HasPrefix(detail.Freshness, "Sem sinal desde") {
		t.Fatalf("stale detail: %+v, %t, %v", detail, found, err)
	}
}

func TestPanelHerdrDownWithoutSnapshot(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	if result, err := store.IngestPresence("dev-1", presenceBody("boot-1", stampAt(1), 0, false), at(2)); err != nil || result.Status != StatusOK {
		t.Fatalf("presence: %+v %v", result, err)
	}
	detail, found, err := store.panelDetail("dev-1", "", at(3))
	if err != nil || !found || detail.HasSnapshot || detail.Freshness != "Herdr sem resposta. A última captura continua aqui." {
		t.Fatalf("detail: %+v, %t, %v", detail, found, err)
	}
}

func TestPanelUsesPresenceReceivedAtBeforeLastSeenFallback(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	if result, err := store.IngestPresence("dev-1", presenceBody("boot-1", stampAt(0), 0, false), at(9)); err != nil || result.Status != StatusOK {
		t.Fatalf("presence: %+v %v", result, err)
	}
	if _, err := store.db.Exec(`UPDATE devices SET last_seen_at=? WHERE id=?`, stampAt(1), "dev-1"); err != nil {
		t.Fatal(err)
	}
	detail, found, err := store.panelDetail("dev-1", "", at(10))
	if err != nil || !found || detail.Freshness != "Herdr sem resposta. A última captura continua aqui." {
		t.Fatalf("detail: %+v, %t, %v", detail, found, err)
	}
	if _, err := store.db.Exec(`DELETE FROM presence WHERE device_id=?`, "dev-1"); err != nil {
		t.Fatal(err)
	}
	detail, found, err = store.panelDetail("dev-1", "", at(10))
	if err != nil || !found || !strings.HasPrefix(detail.Freshness, "Sem sinal desde") || len(detail.Presence.Slots) != 0 {
		t.Fatalf("fallback: %+v, %t, %v", detail, found, err)
	}
}
