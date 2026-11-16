package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"herdr-recall/internal/contract"
)

// reportDiagnostics is best-effort: it never alters the durable FIFO. A failed
// or disconnected report eventually becomes stale on the server.
func reportDiagnostics(ctx context.Context, opt deliveryOptions) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	unlock, err := lockState(ctx, opt.StateDir, ".spool.lock")
	if err != nil {
		return errDelivery
	}
	queue, queueErr := loadSpool(filepath.Join(opt.StateDir, "spool.json"))
	conflicts, conflictErr := loadSpool(filepath.Join(opt.StateDir, "spool.conflict"))
	observed := time.Now().UTC()
	unlock()
	if queueErr != nil || conflictErr != nil {
		return errDelivery
	}
	report := struct {
		ObservedAt    string `json:"observed_at"`
		PendingCount  int    `json:"pending_count"`
		ConflictCount int    `json:"conflict_count"`
	}{contract.FormatTime(observed), len(queue.Items), len(conflicts.Items)}
	body, err := json.Marshal(report)
	if err != nil {
		return errDelivery
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, opt.API+"/v1/diagnostics", bytes.NewReader(body))
	if err != nil {
		return errDelivery
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+opt.Token)
	client := &http.Client{Timeout: 4 * time.Second, CheckRedirect: refuseRedirect}
	response, err := client.Do(request)
	if err != nil {
		return errDelivery
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return errors.New("relato de diagnóstico não confirmado")
	}
	return nil
}
