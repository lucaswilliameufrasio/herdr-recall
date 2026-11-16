package agent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"time"

	"herdr-recall/internal/contract"
)

// ErrDeviceRevoked stops delivery without removing the queued item.
var ErrDeviceRevoked = errors.New("dispositivo não autorizado")

var errDelivery = errors.New("não foi possível enviar a fila")

type deliveryOptions struct {
	StateDir string
	API      string
	Token    string
	GOOS     string
	DirSync  func(string) error
	Client   *http.Client
}

// drainQueue leaves unacknowledged work on disk. Only one sender owns the FIFO
// at a time; the short spool lock is never held across network requests.
func drainQueue(ctx context.Context, opt deliveryOptions) error {
	if opt.StateDir == "" || !tokenOK(opt.Token) {
		return errDelivery
	}
	api, err := CanonicalAPI(opt.API)
	if err != nil {
		return errDelivery
	}
	if opt.DirSync == nil {
		opt.DirSync = syncDir
	}
	if opt.GOOS == "" {
		opt.GOOS = runtime.GOOS
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: refuseRedirect}
	if opt.Client != nil {
		copied := *opt.Client
		copied.CheckRedirect = refuseRedirect
		if copied.Timeout <= 0 {
			copied.Timeout = 10 * time.Second
		}
		client = &copied
	}
	release, err := lockState(ctx, opt.StateDir, ".delivery.lock")
	if err != nil {
		return errDelivery
	}
	defer release()
	queuePath := filepath.Join(opt.StateDir, "spool.json")
	for {
		unlock, err := lockState(ctx, opt.StateDir, ".spool.lock")
		if err != nil {
			return errDelivery
		}
		queue, err := loadSpool(queuePath)
		unlock()
		if err != nil {
			return errDelivery
		}
		if len(queue.Items) == 0 {
			return nil
		}
		item := queue.Items[0]
		endpoint, err := deliveryEndpoint(item)
		if err != nil {
			return errDelivery
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, api+endpoint, bytes.NewReader(item.Body))
		if err != nil {
			return errDelivery
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+opt.Token)
		response, err := client.Do(request)
		if err != nil {
			return errDelivery
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if response.StatusCode == http.StatusUnauthorized {
			return ErrDeviceRevoked
		}
		conflict := response.StatusCode == http.StatusConflict
		if !conflict && (response.StatusCode < 200 || response.StatusCode >= 300) {
			return errDelivery
		}
		if err := acknowledgeItem(ctx, opt, item, conflict); err != nil {
			return errDelivery
		}
	}
}

func deliveryEndpoint(item spoolItem) (string, error) {
	switch item.Kind {
	case kindPresence:
		presence, err := contract.ValidatePresence(item.Body)
		if err != nil || presence.ObservedAt != item.ObservedAt {
			return "", errDelivery
		}
		return "/v1/presence", nil
	case kindSnapshot:
		snapshot, err := contract.ValidateSnapshot(item.Body)
		if err != nil || snapshot.SnapshotID != item.SnapshotID {
			return "", errDelivery
		}
		return "/v1/snapshots", nil
	default:
		return "", errDelivery
	}
}

func acknowledgeItem(ctx context.Context, opt deliveryOptions, item spoolItem, conflict bool) error {
	unlock, err := lockState(ctx, opt.StateDir, ".spool.lock")
	if err != nil {
		return err
	}
	defer unlock()
	queuePath := filepath.Join(opt.StateDir, "spool.json")
	queue, err := loadSpool(queuePath)
	if err != nil || len(queue.Items) == 0 || !sameSpoolItem(queue.Items[0], item) {
		return errDelivery
	}
	if conflict {
		path := filepath.Join(opt.StateDir, "spool.conflict")
		quarantine, err := loadSpool(path)
		if err != nil {
			return err
		}
		found := false
		for _, existing := range quarantine.Items {
			if sameSpoolItem(existing, item) {
				found = true
				break
			}
		}
		if !found {
			quarantine.Items = append(quarantine.Items, item)
			if err := saveSpool(path, quarantine, opt.DirSync); err != nil {
				if kept := continueAfterDirSync(err, opt.GOOS, opt.StateDir, time.Now()); kept != nil {
					return kept
				}
			}
		} else if err := opt.DirSync(opt.StateDir); err != nil {
			if kept := continueAfterDirSync(&dirSyncError{Err: err}, opt.GOOS, opt.StateDir, time.Now()); kept != nil {
				return kept
			}
		}
	}
	queue.Items = queue.Items[1:]
	if err := saveSpool(queuePath, queue, opt.DirSync); err != nil {
		return continueAfterDirSync(err, opt.GOOS, opt.StateDir, time.Now())
	}
	return nil
}

func sameSpoolItem(a, b spoolItem) bool {
	return a.Kind == b.Kind && a.SnapshotID == b.SnapshotID && a.ObservedAt == b.ObservedAt && bytes.Equal(a.Body, b.Body)
}
