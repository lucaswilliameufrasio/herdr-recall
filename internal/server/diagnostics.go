package server

import (
	"net/http"
	"time"

	"herdr-recall/internal/contract"
)

// diagnostics accepts only bounded, non-sensitive aggregate counts. It is
// independent of the capture queue, so a blocked head can still be reported.
func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request) {
	device, ok := s.authorizeDevice(w, r)
	if !ok {
		return
	}
	raw, ok := readLimited(w, r, smallMax)
	if !ok {
		return
	}
	var report struct {
		ObservedAt    string `json:"observed_at"`
		PendingCount  *int   `json:"pending_count"`
		ConflictCount *int   `json:"conflict_count"`
	}
	if err := decodeObject(raw, &report); err != nil || report.PendingCount == nil || report.ConflictCount == nil ||
		*report.PendingCount < 0 || *report.PendingCount > 1000000 || *report.ConflictCount < 0 || *report.ConflictCount > 1000000 {
		writeError(w, StatusUnprocessable, codeInvalidParams, []contract.FieldError{{Field: "body", Message: "diagnóstico inválido"}})
		return
	}
	observed, valid := contract.ParseTime(report.ObservedAt)
	if !valid || observed.After(s.now().Add(time.Minute)) {
		writeError(w, StatusUnprocessable, codeInvalidParams, []contract.FieldError{{Field: "observed_at", Message: "horário inválido ou futuro"}})
		return
	}
	result, err := s.store.db.Exec(`INSERT INTO agent_diagnostics
		(device_id, observed_at, received_at, pending_count, conflict_count)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET observed_at=excluded.observed_at,
		received_at=excluded.received_at, pending_count=excluded.pending_count,
		conflict_count=excluded.conflict_count
		WHERE excluded.observed_at > agent_diagnostics.observed_at`,
		device.ID, stamp(observed), stamp(s.now()), *report.PendingCount, *report.ConflictCount)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	if count == 0 {
		writeError(w, http.StatusConflict, "DIAGNOSTIC_OUTDATED", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
