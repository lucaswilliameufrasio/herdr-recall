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
	id       string
	name     string
	lastSeen sql.NullString
}

func freshness(value sql.NullString, now time.Time) string {
	if !value.Valid {
		return "Sem presença nesta máquina."
	}
	at, ok := contract.ParseTime(value.String)
	if !ok {
		return "Sem informação de presença."
	}
	if now.Sub(at) > 180*time.Second {
		return "Sem sinal desde " + at.UTC().Format("2006-01-02 15:04 UTC") + ". A última captura continua aqui."
	}
	return "Presença recebida em " + at.UTC().Format("2006-01-02 15:04 UTC") + "."
}

func (s *Store) panelMachines(now time.Time) ([]web.Machine, error) {
	rows, err := s.db.Query(`SELECT id, name, last_seen_at FROM devices ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	var devices []panelDevice
	for rows.Next() {
		var device panelDevice
		if err := rows.Scan(&device.id, &device.name, &device.lastSeen); err != nil {
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
		machine := web.Machine{ID: device.id, Name: device.name, Boot: "—", Freshness: freshness(device.lastSeen, now)}
		var boot, payload string
		err := s.db.QueryRow(`SELECT b.boot_id, s.payload FROM boots b JOIN snapshots s ON s.device_id=b.device_id AND s.boot_id=b.boot_id WHERE b.device_id=? ORDER BY b.first_received_at DESC, b.boot_id ASC LIMIT 1`, device.id).Scan(&boot, &payload)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			snapshot, err := contract.ValidateSnapshot([]byte(payload))
			if err != nil {
				return nil, fmt.Errorf("captura inválida: %w", err)
			}
			machine.Boot = boot
			machine.PaneCount = len(snapshot.Panes)
		}
		machines = append(machines, machine)
	}
	return machines, nil
}

func (s *Store) panelDetail(id, selected string, now time.Time) (web.Detail, bool, error) {
	var name string
	var lastSeen sql.NullString
	err := s.db.QueryRow(`SELECT name, last_seen_at FROM devices WHERE id=?`, id).Scan(&name, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return web.Detail{}, false, nil
	}
	if err != nil {
		return web.Detail{}, false, err
	}
	detail := web.Detail{ID: id, Name: name, Freshness: freshness(lastSeen, now)}
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
	}
	rows, err = s.db.Query(`SELECT observed_at FROM presence WHERE device_id=? ORDER BY received_at DESC, observed_at DESC`, id)
	if err != nil {
		return detail, false, err
	}
	var observed []time.Time
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return detail, false, err
		}
		at, ok := contract.ParseTime(raw)
		if !ok {
			_ = rows.Close()
			return detail, false, fmt.Errorf("presença inválida")
		}
		observed = append(observed, at)
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
	return detail, true, nil
}
