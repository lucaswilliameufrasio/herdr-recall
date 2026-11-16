package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerifyDeliverySendsFIFOAndForcesSnapshot(t *testing.T) {
	home := t.TempDir()
	opt := testOptions(filepath.Join(home, "state"), herdrRaw(sessionPane), time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	serviceTestEnrollment(t, home, server.URL)
	opt.Now = opt.Now.Add(time.Minute)
	if err := VerifyDelivery(context.Background(), home, opt); err != nil {
		t.Fatal(err)
	}
	want := "/v1/presence,/v1/snapshots,/v1/presence,/v1/snapshots"
	if strings.Join(paths, ",") != want {
		t.Fatalf("ordem incorreta: %v", paths)
	}
	queue := readSpoolFile(t, opt.StateDir)
	if len(queue.Items) != 0 {
		t.Fatal("fila não foi reconhecida")
	}
}

func TestVerifyDeliveryReportsFailuresWithoutLosingQueue(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusServiceUnavailable, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			home := t.TempDir()
			opt := testOptions(filepath.Join(home, "state"), herdrRaw(sessionPane), time.Now())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if status == http.StatusConflict && r.URL.Path == "/v1/presence" {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			serviceTestEnrollment(t, home, server.URL)
			err := VerifyDelivery(context.Background(), home, opt)
			if err == nil {
				t.Fatal("falha não foi detectada")
			}
			if status == http.StatusUnauthorized && !errors.Is(err, ErrDeviceRevoked) {
				t.Fatal("revogação não foi identificada")
			}
			if status == http.StatusConflict {
				if !errors.Is(err, ErrDeliveryConflict) {
					t.Fatal("conflito não foi identificado")
				}
				conflicts, err := loadSpool(filepath.Join(opt.StateDir, "spool.conflict"))
				if err != nil || len(conflicts.Items) != 1 {
					t.Fatal("captura não foi preservada na quarentena")
				}
			} else if len(readSpoolFile(t, opt.StateDir).Items) != 2 {
				t.Fatal("fila não foi preservada")
			} else if !errors.Is(err, ErrDeliveryPending) {
				t.Fatal("item pendente não foi identificado")
			}
		})
	}
}

func TestVerifyStopsOnEarlierConflictInsteadOfReportingSuccess(t *testing.T) {
	home := t.TempDir()
	opt := testOptions(filepath.Join(home, "state"), herdrRaw(sessionPane), time.Now())
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.Copy(io.Discard, r.Body)
		if calls == 1 {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	serviceTestEnrollment(t, home, server.URL)
	opt.Now = opt.Now.Add(time.Minute)
	err := VerifyDelivery(context.Background(), home, opt)
	if !errors.Is(err, ErrDeliveryConflict) || calls != 1 {
		t.Fatal("conflito anterior não interrompeu a verificação")
	}
	if len(readSpoolFile(t, opt.StateDir).Items) != 3 {
		t.Fatal("itens seguintes não permaneceram na fila")
	}
	before := calls
	err = VerifyDelivery(context.Background(), home, opt)
	if !errors.Is(err, ErrDeliveryConflict) || calls != before || len(readSpoolFile(t, opt.StateDir).Items) != 3 {
		t.Fatal("quarentena preexistente não impediu nova captura e envio")
	}
}

func TestDeliveryReportsConflictAndSubsequentFailure(t *testing.T) {
	dir := t.TempDir()
	opt := testOptions(dir, herdrRaw(sessionPane), time.Now())
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	err := drainQueue(context.Background(), deliveryOptions{StateDir: dir, API: server.URL, Token: "synthetic-token", GOOS: "linux"})
	if !errors.Is(err, ErrDeliveryConflict) || !errors.Is(err, ErrDeliveryPending) {
		t.Fatal("conflito e falha posterior devem ser informados")
	}
	if len(readSpoolFile(t, dir).Items) != 1 {
		t.Fatal("item não confirmado não permaneceu na fila")
	}
}

func TestDeliveryRejectsErrorEnvelopeWithSuccessStatus(t *testing.T) {
	dir := t.TempDir()
	opt := testOptions(dir, herdrRaw(sessionPane), time.Now())
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"error":"UNEXPECTED_ERROR"}`)
	}))
	defer server.Close()
	err := drainQueue(context.Background(), deliveryOptions{StateDir: dir, API: server.URL, Token: "synthetic-token", GOOS: "linux"})
	if !errors.Is(err, ErrDeliveryPending) || len(readSpoolFile(t, dir).Items) != 2 {
		t.Fatal("resposta contraditória foi aceita ou removeu a fila")
	}
}

func TestVerifyDeliveryDoesNotSendWhenHerdrFails(t *testing.T) {
	home := t.TempDir()
	opt := testOptions(filepath.Join(home, "state"), nil, time.Now())
	opt.Herdr = func(context.Context) ([]byte, error) { return nil, errors.New("fixture") }
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	serviceTestEnrollment(t, home, server.URL)
	if err := VerifyDelivery(context.Background(), home, opt); err == nil || calls != 0 {
		t.Fatal("Herdr indisponível não interrompeu o envio")
	}
	if len(readSpoolFile(t, opt.StateDir).Items) != 1 {
		t.Fatal("presença não foi preservada")
	}
}
