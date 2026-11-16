package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServiceInstallRequiresConfigAndTokenWithoutSideEffects(t *testing.T) {
	home := t.TempDir()
	runner := &fakeCmd{}
	manager := testManager(home, filepath.Join(home, "herdr-recall"), "", runner)
	for _, dry := range []bool{false, true} {
		if _, err := manager.Install(context.Background(), InstallOptions{DryRun: dry}); !errors.Is(err, ErrServiceConfig) {
			t.Fatalf("config ausente dry=%v: %v", dry, err)
		}
	}
	paths := PathsFor(home)
	if err := Save(paths, "http://127.0.0.1:12345", "test-token", nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(context.Background(), InstallOptions{}); !errors.Is(err, ErrServiceToken) {
		t.Fatalf("token ausente: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("executou comando antes da validação")
	}
	unit, err := manager.FilePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unit); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("gravou unidade sem credenciais")
	}
}

func TestDoctorChecksHealthThenDeviceWithoutLeakingCredentials(t *testing.T) {
	const secret = "test-token-private"
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/healthz":
			if r.Header.Get("Authorization") != "" {
				t.Error("health recebeu token")
			}
			_, _ = w.Write([]byte("ok"))
		case "/v1/device":
			if r.Header.Get("Authorization") != "Bearer "+secret {
				t.Error("autorização ausente")
			}
			_, _ = w.Write([]byte(`{"id":"d1","name":"Máquina","created_at":"2026-11-13T00:00:00Z"}`))
		default:
			t.Error("rota inesperada")
		}
	}))
	defer server.Close()
	home := t.TempDir()
	if err := Save(PathsFor(home), server.URL, secret, nil); err != nil {
		t.Fatal(err)
	}
	manager := testManager(home, filepath.Join(home, "herdr-recall"), "", &fakeCmd{})
	checks := manager.Doctor(context.Background())
	if !hasCheck(checks, "servidor", StatusOK) || !hasCheck(checks, "token", StatusOK) {
		t.Fatalf("checks não confirmaram servidor e token")
	}
	if strings.Join(paths, ",") != "/healthz,/v1/device" {
		t.Fatalf("ordem de consultas incorreta")
	}
	for _, check := range checks {
		if strings.Contains(check.Detail, secret) || strings.Contains(check.Fix, secret) {
			t.Fatal("doctor expôs token")
		}
	}
}

func TestDoctorServerFailuresAndRedirects(t *testing.T) {
	for _, tc := range []struct {
		name         string
		healthStatus int
		deviceStatus int
		deviceBody   string
		wantName     string
		wantStatus   Status
		deviceCalls  int
	}{
		{"health failure stops", 503, 200, "", "servidor", StatusFail, 0},
		{"revoked", 200, 401, `{"error":"DEVICE_REVOKED"}`, "token", StatusFail, 1},
		{"old server", 200, 404, "", "token", StatusWarn, 1},
		{"invalid response", 200, 200, `{"name":"only"}`, "token", StatusFail, 1},
		{"server error", 200, 500, "", "token", StatusFail, 1},
		{"oversized response", 200, 200, strings.Repeat("x", doctorBodyLimit+1), "token", StatusFail, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deviceCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/healthz" {
					w.WriteHeader(tc.healthStatus)
					_, _ = w.Write([]byte("ok"))
					return
				}
				deviceCalls++
				w.WriteHeader(tc.deviceStatus)
				_, _ = w.Write([]byte(tc.deviceBody))
			}))
			defer server.Close()
			home := t.TempDir()
			serviceTestEnrollment(t, home, server.URL)
			manager := testManager(home, filepath.Join(home, "herdr-recall"), "", &fakeCmd{})
			checks := manager.Doctor(context.Background())
			if !hasCheck(checks, tc.wantName, tc.wantStatus) || deviceCalls != tc.deviceCalls {
				t.Fatalf("status ou chamadas inesperadas: %d", deviceCalls)
			}
		})
	}
	t.Run("redirect does not forward token", func(t *testing.T) {
		forwarded := false
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded = true }))
		defer target.Close()
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				_, _ = w.Write([]byte("ok"))
				return
			}
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
		}))
		defer origin.Close()
		home := t.TempDir()
		serviceTestEnrollment(t, home, origin.URL)
		manager := testManager(home, filepath.Join(home, "herdr-recall"), "", &fakeCmd{})
		checks := manager.Doctor(context.Background())
		if forwarded || !hasCheck(checks, "token", StatusFail) {
			t.Fatal("redirecionamento seguido ou não detectado")
		}
	})
}

func TestDoctorHandlesCancellationWithoutLeakingError(t *testing.T) {
	home := t.TempDir()
	serviceTestEnrollment(t, home, "http://127.0.0.1:12345")
	manager := testManager(home, filepath.Join(home, "herdr-recall"), "", &fakeCmd{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	checks := manager.Doctor(ctx)
	if !hasCheck(checks, "servidor", StatusFail) {
		t.Fatal("cancelamento não foi registrado")
	}
}

func TestServiceRejectsInvalidConfigAndTokenFiles(t *testing.T) {
	home := t.TempDir()
	manager := testManager(home, filepath.Join(home, "herdr-recall"), "", &fakeCmd{})
	serviceTestEnrollment(t, home, "http://127.0.0.1:12345")
	paths := PathsFor(home)
	if err := os.Chmod(paths.Token, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(context.Background(), InstallOptions{}); !errors.Is(err, ErrServiceToken) {
		t.Fatal("token acessível por outros usuários foi aceito")
	}
	if err := os.Chmod(paths.Token, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Token, []byte("bad token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(context.Background(), InstallOptions{}); !errors.Is(err, ErrServiceToken) {
		t.Fatal("token malformado foi aceito")
	}
	if err := os.WriteFile(paths.Config, []byte(`{"api":"bad","token_file":"relative"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(context.Background(), InstallOptions{}); !errors.Is(err, ErrServiceConfig) {
		t.Fatal("configuração inválida foi aceita")
	}
	if err := Save(paths, "http://example.invalid", "test-token", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(context.Background(), InstallOptions{}); !errors.Is(err, ErrServiceConfig) {
		t.Fatal("HTTP fora de loopback foi aceito")
	}
}

func TestFailedReinstallDoesNotModifyExistingUnit(t *testing.T) {
	home := t.TempDir()
	runner := &fakeCmd{}
	manager := testManager(home, filepath.Join(home, "herdr-recall"), "", runner)
	unit, err := manager.FilePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("existing unit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(context.Background(), InstallOptions{}); !errors.Is(err, ErrServiceConfig) {
		t.Fatal("reinstalação sem config não foi recusada")
	}
	content, err := os.ReadFile(unit)
	if err != nil || string(content) != "existing unit" || len(runner.calls) != 0 {
		t.Fatal("unidade ou serviço alterado antes da validação")
	}
}

func TestDoctorCountsQueueAndConflictsWithoutExposingPayload(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(home, ".local", "state", "herdr-recall")
	opt := testOptions(stateDir, herdrRaw(sessionPane), time.Time{})
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	queue, err := loadSpool(filepath.Join(stateDir, "spool.json"))
	if err != nil || len(queue.Items) != 2 {
		t.Fatal("fixture da fila inválida")
	}
	if err := saveSpool(filepath.Join(stateDir, "spool.conflict"), spoolFile{Items: []spoolItem{queue.Items[1]}}, syncDir); err != nil {
		t.Fatal(err)
	}
	manager := testManager(home, filepath.Join(home, "herdr-recall"), "", &fakeCmd{})
	checks := manager.queueChecks()
	if !hasCheck(checks, "fila", StatusOK) || !hasCheck(checks, "conflitos", StatusWarn) {
		t.Fatal("doctor não mostrou fila e conflito")
	}
	for _, check := range checks {
		if strings.Contains(check.Detail, "ses_abcdef12") || strings.Contains(check.Detail, "/home/project") {
			t.Fatal("doctor expôs conteúdo da fila")
		}
	}
}
