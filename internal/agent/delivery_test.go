package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDrainQueueFIFOSuccessAndConflict(t *testing.T) {
	dir := t.TempDir()
	opt := testOptions(dir, herdrRaw(sessionPane), time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	opt.Now = opt.Now.Add(time.Minute)
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	queuePath := filepath.Join(dir, "spool.json")
	before, err := loadSpool(queuePath)
	if err != nil || len(before.Items) != 3 {
		t.Fatal("fixture sem presença e snapshot")
	}
	var bodies [][]byte
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		paths = append(paths, r.URL.Path)
		if len(paths) == 2 {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	err = drainQueue(context.Background(), deliveryOptions{StateDir: dir, API: server.URL, Token: "synthetic-token", GOOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != "/v1/presence,/v1/snapshots,/v1/presence" || len(bodies) != 3 || !bytes.Equal(bodies[0], before.Items[0].Body) || !bytes.Equal(bodies[1], before.Items[1].Body) || !bytes.Equal(bodies[2], before.Items[2].Body) {
		t.Fatal("fila não foi enviada em ordem com os bytes gravados")
	}
	after, err := loadSpool(queuePath)
	if err != nil || len(after.Items) != 0 {
		t.Fatal("fila ativa não foi esvaziada")
	}
	conflict, err := loadSpool(filepath.Join(dir, "spool.conflict"))
	if err != nil || len(conflict.Items) != 1 || !bytes.Equal(conflict.Items[0].Body, before.Items[1].Body) {
		t.Fatal("item conflitante não foi preservado")
	}
}

func TestDrainSendsOldBootBeforeNewBoot(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	opt := testOptions(dir, herdrRaw(sessionPane), now)
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	opt.BootID = "boot-2"
	opt.Now = now.Add(time.Minute)
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	var order []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			BootID string `json:"boot_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error("corpo inválido")
		}
		order = append(order, r.URL.Path+":"+body.BootID)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := drainQueue(context.Background(), deliveryOptions{StateDir: dir, API: server.URL, Token: "synthetic-token", GOOS: "linux"}); err != nil {
		t.Fatal(err)
	}
	want := "/v1/presence:boot-1,/v1/snapshots:boot-1,/v1/presence:boot-2,/v1/snapshots:boot-2"
	if strings.Join(order, ",") != want {
		t.Fatalf("ordem de boots incorreta: %v", order)
	}
}

func TestDrainQueueRevokedPreservesQueue(t *testing.T) {
	dir := t.TempDir()
	opt := testOptions(dir, herdrRaw(sessionPane), time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	before, err := loadSpool(filepath.Join(dir, "spool.json"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	err = drainQueue(context.Background(), deliveryOptions{StateDir: dir, API: server.URL, Token: "synthetic-token", GOOS: "linux"})
	if !errors.Is(err, ErrDeviceRevoked) || calls != 1 {
		t.Fatal("revogação não interrompeu envio")
	}
	after, err := loadSpool(filepath.Join(dir, "spool.json"))
	if err != nil || len(after.Items) != len(before.Items) || !bytes.Equal(after.Items[0].Body, before.Items[0].Body) {
		t.Fatal("fila foi alterada após revogação")
	}
}

func TestCorruptQueueRemainsUntouchedAndNeverSent(t *testing.T) {
	for _, body := range []string{`{"items":[]} trailing`, `{"items":null}`, `{}`} {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "spool.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			err := drainQueue(context.Background(), deliveryOptions{StateDir: dir, API: server.URL, Token: "synthetic-token", GOOS: "linux"})
			if err == nil || calls != 0 {
				t.Fatal("fila corrompida aceita")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != body {
				t.Fatal("fila corrompida foi alterada")
			}
		})
	}
}

func TestDrainRetryPreservesFrozenBody(t *testing.T) {
	dir := t.TempDir()
	opt := testOptions(dir, herdrRaw(sessionPane), time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	queuePath := filepath.Join(dir, "spool.json")
	before, err := loadSpool(queuePath)
	if err != nil {
		t.Fatal(err)
	}
	var attempts [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		attempts = append(attempts, body)
		if len(attempts) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	delivery := deliveryOptions{StateDir: dir, API: server.URL, Token: "synthetic-token", GOOS: "linux"}
	if err := drainQueue(context.Background(), delivery); err == nil {
		t.Fatal("falha transitória não foi detectada")
	}
	after, err := loadSpool(queuePath)
	if err != nil || len(after.Items) != len(before.Items) || !bytes.Equal(after.Items[0].Body, before.Items[0].Body) {
		t.Fatal("falha alterou a cabeça")
	}
	if err := drainQueue(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if len(attempts) < 2 || !bytes.Equal(attempts[0], attempts[1]) {
		t.Fatal("retentativa alterou os bytes")
	}
}

func TestConflictAlreadyInQuarantineDoesNotDuplicate(t *testing.T) {
	dir := t.TempDir()
	opt := testOptions(dir, herdrRaw(sessionPane), time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	queue, err := loadSpool(filepath.Join(dir, "spool.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := saveSpool(filepath.Join(dir, "spool.conflict"), spoolFile{Items: []spoolItem{queue.Items[0]}}, syncDir); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := drainQueue(context.Background(), deliveryOptions{StateDir: dir, API: server.URL, Token: "synthetic-token", GOOS: "linux"}); err != nil {
		t.Fatal(err)
	}
	conflict, err := loadSpool(filepath.Join(dir, "spool.conflict"))
	if err != nil || len(conflict.Items) != 1 {
		t.Fatal("repetição duplicou o item em quarentena")
	}
}

func TestConflictSyncFailureKeepsHeadAndRetryDoesNotDuplicate(t *testing.T) {
	dir := t.TempDir()
	opt := testOptions(dir, herdrRaw(sessionPane), time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls <= 3 {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	delivery := deliveryOptions{
		StateDir: dir, API: server.URL, Token: "synthetic-token", GOOS: "linux",
		DirSync: func(string) error { return errors.New("synthetic sync failure") },
	}
	if err := drainQueue(context.Background(), delivery); err == nil || calls != 1 {
		t.Fatal("falha de sync não interrompeu envio")
	}
	queue, err := loadSpool(filepath.Join(dir, "spool.json"))
	if err != nil || len(queue.Items) != 2 {
		t.Fatal("head não foi preservada")
	}
	if err := drainQueue(context.Background(), delivery); err == nil || calls != 2 {
		t.Fatal("sync da quarentena existente não foi exigido")
	}
	queue, err = loadSpool(filepath.Join(dir, "spool.json"))
	if err != nil || len(queue.Items) != 2 {
		t.Fatal("falha repetida removeu a cabeça")
	}
	delivery.DirSync = syncDir
	if err := drainQueue(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	conflict, err := loadSpool(filepath.Join(dir, "spool.conflict"))
	if err != nil || len(conflict.Items) != 1 {
		t.Fatal("retentativa duplicou quarentena")
	}
}

func TestCaptureCanPersistWhileHTTPIsBlocked(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	opt := testOptions(dir, herdrRaw(sessionPane), now)
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/presence" {
			select {
			case <-started:
			default:
				close(started)
				<-release
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- drainQueue(ctx, deliveryOptions{StateDir: dir, API: server.URL, Token: "synthetic-token", GOOS: "linux"})
	}()
	<-started
	opt.Now = now.Add(time.Minute)
	captureDone := make(chan error, 1)
	go func() {
		_, err := Capture(ctx, opt)
		captureDone <- err
	}()
	select {
	case err := <-captureDone:
		if err != nil {
			close(release)
			t.Fatal(err)
		}
	case <-ctx.Done():
		close(release)
		t.Fatal("HTTP bloqueou a gravação local")
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestRunDeliveryPersistsBeforeHTTPAndStopsOnRevocation(t *testing.T) {
	dir := t.TempDir()
	opt := testOptions(dir, herdrRaw(sessionPane), time.Time{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if _, err := os.Stat(filepath.Join(dir, "latest.json")); err != nil {
			t.Error("HTTP começou antes da captura local")
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-token" {
			t.Error("bearer ausente")
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	var log bytes.Buffer
	err := runDelivery(ctx, opt, time.Hour, &log, deliveryOptions{StateDir: dir, API: server.URL, Token: "synthetic-token", GOOS: "linux"})
	if !errors.Is(err, ErrDeviceRevoked) || calls != 1 {
		t.Fatal("revogação não interrompeu o loop")
	}
	queue, err := loadSpool(filepath.Join(dir, "spool.json"))
	if err != nil || len(queue.Items) != 2 {
		t.Fatal("revogação removeu a fila")
	}
	if strings.Contains(log.String(), "synthetic-token") || strings.Contains(log.String(), "ses_abcdef12") || strings.Contains(log.String(), "/home/project") {
		t.Fatal("log vazou dados")
	}
}

func TestRunDeliveryNeverSendsAfterCaptureSyncFailure(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opt := testOptions(dir, herdrRaw(sessionPane), time.Time{})
	opt.DirSync = func(string) error { return errors.New("synthetic sync failure") }
	captures := 0
	opt.Herdr = func(context.Context) ([]byte, error) {
		captures++
		if captures == 2 {
			cancel()
		}
		return herdrRaw(sessionPane), nil
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := runDelivery(ctx, opt, 20*time.Millisecond, io.Discard, deliveryOptions{StateDir: dir, API: server.URL, Token: "synthetic-token", GOOS: "linux"}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("enviou apesar de falha de sincronização")
	}
}

func TestRunDeliveryCapturesAgainWhenHTTPIsSlow(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{}, 1)
	secondCapture := make(chan struct{}, 1)
	opt := testOptions(dir, herdrRaw(sessionPane), time.Time{})
	captures := 0
	opt.Herdr = func(context.Context) ([]byte, error) {
		captures++
		if captures == 2 {
			secondCapture <- struct{}{}
		}
		return herdrRaw(sessionPane), nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		done <- runDelivery(ctx, opt, 100*time.Millisecond, io.Discard, deliveryOptions{StateDir: dir, API: server.URL, Token: "synthetic-token", GOOS: "linux"})
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("requisição não iniciou")
	}
	select {
	case <-secondCapture:
	case <-ctx.Done():
		t.Fatal("rede lenta bloqueou a próxima captura")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	queue, err := loadSpool(filepath.Join(dir, "spool.json"))
	if err != nil || len(queue.Items) == 0 {
		t.Fatal("cancelamento de requisição descartou a fila")
	}
}
