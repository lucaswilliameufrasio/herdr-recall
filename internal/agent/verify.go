package agent

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"runtime"
	"time"
)

// VerifyDelivery captures and queues a fresh snapshot, then sends the FIFO
// through that snapshot. The caller must explicitly authorize these writes.
// Success means the server acknowledged the snapshot, not that a panel or
// restoration was independently inspected.
func VerifyDelivery(ctx context.Context, home string, opt CaptureOptions) error {
	api, token, err := (Manager{Home: home}).serviceCredentials()
	if err != nil {
		return err
	}
	unlock, err := lockState(ctx, opt.StateDir, ".delivery.lock")
	if err != nil {
		return errors.New("não foi possível adquirir o emissor")
	}
	defer unlock()

	queueUnlock, err := lockState(ctx, opt.StateDir, ".spool.lock")
	if err != nil {
		return errors.New("não foi possível verificar a quarentena; nenhuma captura foi iniciada")
	}
	conflicts, conflictErr := loadSpool(filepath.Join(opt.StateDir, "spool.conflict"))
	queueUnlock()
	if conflictErr != nil {
		return errors.New("não foi possível ler a quarentena; nenhuma captura foi iniciada")
	}
	if len(conflicts.Items) > 0 {
		return ErrDeliveryConflict
	}

	id, err := newSnapshotID()
	if err != nil {
		return errors.New("não foi possível preparar a captura")
	}
	opt.ForceSnapshot = true
	opt.NewID = func() (string, error) {
		return id, nil
	}
	result, err := Capture(ctx, opt)
	if err != nil {
		return errors.New("não foi possível gravar a captura; a fila pode conter itens novos")
	}
	if !result.HerdrOK {
		return errors.New("Herdr não respondeu; a presença foi gravada, mas nenhuma captura foi enviada")
	}
	goos := opt.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	dirSync := opt.DirSync
	if dirSync == nil {
		dirSync = syncDir
	}
	delivery := deliveryOptions{StateDir: opt.StateDir, API: api, Token: token, GOOS: goos, DirSync: dirSync}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: refuseRedirect}
	if err := drainQueueLocked(ctx, delivery, client, api, id); err != nil {
		if errors.Is(err, ErrDeliveryConflict) || errors.Is(err, ErrDeliveryPending) || errors.Is(err, ErrDeliveryUncertain) {
			return err
		}
		return errors.New("captura não confirmada; confira a fila e a quarentena com service doctor")
	}
	return nil
}
