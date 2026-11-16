package server

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"herdr-recall/internal/contract"
	"herdr-recall/internal/web"
)

type panelDevice struct {
	id   string
	name string
}

func panelState(now time.Time, lastSeen sql.NullString, snapshot, previous, newestHasSnapshot, present bool, received time.Time, herdrOK bool, captured time.Time) string {
	if lastSeen.Valid {
		at, ok := contract.ParseTime(lastSeen.String)
		if ok && now.Sub(at) > 180*time.Second {
			return "Sem sinal desde " + at.UTC().Format("2006-01-02 15:04 UTC") + ". A última captura continua aqui."
		}
	}
	if snapshot && previous {
		if !newestHasSnapshot {
			return "Boot anterior. A mesa nova ainda não chegou."
		}
		return "Boot anterior."
	}
	if present && now.Sub(received) <= 180*time.Second && !herdrOK {
		return "Herdr sem resposta. A última captura continua aqui."
	}
	if snapshot && present && now.Sub(received) <= 180*time.Second && herdrOK {
		if now.Sub(captured) > 180*time.Second {
			return "Mesa estável desde " + captured.UTC().Format("2006-01-02 15:04 UTC") + "."
		}
		minutes := int(now.Sub(captured).Minutes())
		if minutes < 0 {
			minutes = 0
		}
		return fmt.Sprintf("Vista há %d min.", minutes)
	}
	if !lastSeen.Valid {
		return "Sem presença nesta máquina."
	}
	return ""
}

func (s *Store) panelMachines(now time.Time) ([]web.Machine, error) {
	rows, err := s.db.Query(`SELECT id, name FROM devices ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	var devices []panelDevice
	for rows.Next() {
		var device panelDevice
		if err := rows.Scan(&device.id, &device.name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	machines := make([]web.Machine, 0, len(devices))
	for _, device := range devices {
		detail, _, err := s.panelDetail(device.id, "", now)
		if err != nil {
			return nil, err
		}
		machine := web.Machine{ID: device.id, Name: device.name, Boot: "—", Freshness: detail.Freshness, Revoked: detail.Revoked, Diagnostics: detail.Diagnostics}
		if detail.HasSnapshot {
			machine.Boot = detail.Boot
			machine.PaneCount = len(detail.Panes)
		}
		machines = append(machines, machine)
	}
	return machines, nil
}

func (s *Store) panelDetail(id, selected string, now time.Time) (web.Detail, bool, error) {
	var name string
	var lastSeen, revoked sql.NullString
	err := s.db.QueryRow(`SELECT name, last_seen_at, revoked_at FROM devices WHERE id=?`, id).Scan(&name, &lastSeen, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return web.Detail{}, false, nil
	}
	if err != nil {
		return web.Detail{}, false, err
	}
	detail := web.Detail{ID: id, Name: name, Revoked: revoked.Valid}
	detail.Diagnostics = "Estado da fila e quarentena indisponível; consulte o doctor na máquina."
	var observedAt, receivedAt string
	var pendingCount, conflictCount int
	err = s.db.QueryRow(`SELECT observed_at, received_at, pending_count, conflict_count FROM agent_diagnostics WHERE device_id=?`, id).
		Scan(&observedAt, &receivedAt, &pendingCount, &conflictCount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return detail, false, err
	}
	if err == nil {
		observed, observedOK := contract.ParseTime(observedAt)
		received, receivedOK := contract.ParseTime(receivedAt)
		if !observedOK || !receivedOK {
			return detail, false, errors.New("diagnóstico inválido")
		}
		state := "Recente"
		if now.Sub(received) > 3*time.Minute || now.Sub(observed) > 3*time.Minute || observed.Sub(now) > time.Minute || received.Sub(now) > time.Minute {
			state = "Antigo ou relógio divergente"
		}
		detail.Diagnostics = fmt.Sprintf("%s: %d itens pendentes, %d em quarentena; observado em %s UTC. Contagens podem ter mudado; confirme com o doctor.", state, pendingCount, conflictCount, observed.UTC().Format("2006-01-02 15:04:05"))
	}
	rows, err := s.db.Query(`SELECT boot_id, first_received_at FROM boots WHERE device_id=? ORDER BY first_received_at DESC, boot_id ASC`, id)
	if err != nil {
		return detail, false, err
	}
	found := selected == ""
	for rows.Next() {
		var boot web.Boot
		if err := rows.Scan(&boot.ID, &boot.FirstReceived); err != nil {
			_ = rows.Close()
			return detail, false, err
		}
		if boot.ID == selected {
			found = true
		}
		detail.Boots = append(detail.Boots, boot)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return detail, false, err
	}
	if err := rows.Close(); err != nil {
		return detail, false, err
	}
	if !found {
		return detail, false, nil
	}
	query := `SELECT s.boot_id, s.payload FROM snapshots s JOIN boots b ON s.device_id=b.device_id AND s.boot_id=b.boot_id WHERE s.device_id=? ORDER BY b.first_received_at DESC, b.boot_id ASC LIMIT 1`
	args := []any{id}
	if selected != "" {
		query = `SELECT boot_id, payload FROM snapshots WHERE device_id=? AND boot_id=?`
		args = append(args, selected)
	}
	var payload string
	var captured time.Time
	err = s.db.QueryRow(query, args...).Scan(&detail.Boot, &payload)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return detail, false, err
	}
	if err == nil {
		snapshot, err := contract.ValidateSnapshot([]byte(payload))
		if err != nil {
			return detail, false, fmt.Errorf("captura inválida: %w", err)
		}
		detail.HasSnapshot = true
		detail.Panes = web.DeskPanes(snapshot)
		var ok bool
		captured, ok = contract.ParseTime(snapshot.CapturedAt)
		if !ok {
			return detail, false, fmt.Errorf("hora da captura inválida")
		}
	}
	newestHasSnapshot := false
	previous := false
	newestBoots := make(map[string]bool)
	if len(detail.Boots) > 0 {
		newestTime := detail.Boots[0].FirstReceived
		for _, boot := range detail.Boots {
			if boot.FirstReceived == newestTime {
				newestBoots[boot.ID] = true
			}
		}
		for boot := range newestBoots {
			var exists int
			err := s.db.QueryRow(`SELECT 1 FROM snapshots WHERE device_id=? AND boot_id=?`, id, boot).Scan(&exists)
			if err == nil {
				newestHasSnapshot = true
			} else if !errors.Is(err, sql.ErrNoRows) {
				return detail, false, err
			}
		}
		previous = detail.HasSnapshot && !newestBoots[detail.Boot]
	}
	for i := range detail.Boots {
		detail.Boots[i].Selected = (selected != "" && detail.Boots[i].ID == selected) || (selected == "" && detail.Boots[i].ID == detail.Boot)
		detail.Boots[i].Previous = !newestBoots[detail.Boots[i].ID]
	}
	rows, err = s.db.Query(`SELECT observed_at, received_at, boot_id, herdr_ok FROM presence WHERE device_id=? ORDER BY received_at DESC, observed_at DESC`, id)
	if err != nil {
		return detail, false, err
	}
	var observed []time.Time
	var newestReceived time.Time
	var newestPresent, newestHerdrOK bool
	for rows.Next() {
		var raw, receivedRaw, bootID string
		var herdrOK bool
		if err := rows.Scan(&raw, &receivedRaw, &bootID, &herdrOK); err != nil {
			_ = rows.Close()
			return detail, false, err
		}
		if len(observed) == 0 {
			lastSeen = sql.NullString{String: receivedRaw, Valid: true}
		}
		at, ok := contract.ParseTime(raw)
		if !ok {
			_ = rows.Close()
			return detail, false, fmt.Errorf("presença inválida")
		}
		observed = append(observed, at)
		if newestBoots[bootID] && !newestPresent {
			var ok bool
			newestReceived, ok = contract.ParseTime(receivedRaw)
			if !ok {
				_ = rows.Close()
				return detail, false, fmt.Errorf("hora de presença inválida")
			}
			newestPresent = true
			newestHerdrOK = herdrOK
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return detail, false, err
	}
	if err := rows.Close(); err != nil {
		return detail, false, err
	}
	if len(observed) > 0 {
		detail.Presence = web.Chart(observed[0], observed)
	}
	detail.Freshness = panelState(now, lastSeen, detail.HasSnapshot, previous, newestHasSnapshot, newestPresent, newestReceived, newestHerdrOK, captured)
	return detail, true, nil
}
