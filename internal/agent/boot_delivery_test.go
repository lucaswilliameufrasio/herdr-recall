package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"herdr-recall/internal/contract"
)

func TestCaptureQueuesSameDeskForNewBoot(t *testing.T) {
	for _, oldQueueDrained := range []bool{false, true} {
		t.Run(map[bool]string{false: "old boot pending", true: "old boot drained"}[oldQueueDrained], func(t *testing.T) {
			dir := t.TempDir()
			now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
			opt := testOptions(dir, herdrRaw(sessionPane), now)
			if _, err := Capture(context.Background(), opt); err != nil {
				t.Fatal(err)
			}
			spoolPath := filepath.Join(dir, "spool.json")
			if oldQueueDrained {
				if err := saveSpool(spoolPath, spoolFile{}, syncDir); err != nil {
					t.Fatal(err)
				}
			}
			opt.BootID = "boot-2"
			opt.Now = now.Add(time.Minute)
			if _, err := Capture(context.Background(), opt); err != nil {
				t.Fatal(err)
			}
			queue, err := loadSpool(spoolPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(queue.Items) < 2 || queue.Items[len(queue.Items)-2].Kind != kindPresence || queue.Items[len(queue.Items)-1].Kind != kindSnapshot {
				t.Fatal("boot novo não recebeu presença seguida de snapshot")
			}
			snapshot, err := contract.ValidateSnapshot(queue.Items[len(queue.Items)-1].Body)
			if err != nil || snapshot.BootID != "boot-2" {
				t.Fatal("snapshot do boot novo ausente ou inválido")
			}
		})
	}
}

func TestQuietDeskStillRecordsEveryMinuteAfterDelivery(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	opt := testOptions(dir, herdrRaw(sessionPane), now)
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	if err := saveSpool(filepath.Join(dir, "spool.json"), spoolFile{}, syncDir); err != nil {
		t.Fatal(err)
	}
	opt.Now = now.Add(time.Minute)
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	latest := readLatest(t, dir)
	if latest.CapturedAt != contract.FormatTime(opt.Now) {
		t.Fatal("mesa quieta não atualizou latest")
	}
	history, err := os.ReadFile(filepath.Join(dir, "history", "20261113.jsonl"))
	if err != nil || bytes.Count(history, []byte("\n")) != 2 {
		t.Fatal("histórico não registrou o minuto")
	}
	queue := readSpoolFile(t, dir)
	if len(queue.Items) != 1 || queue.Items[0].Kind != kindPresence {
		t.Fatal("mesa quieta gerou envio de snapshot")
	}
}
