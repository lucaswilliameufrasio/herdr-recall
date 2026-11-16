package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"herdr-recall/internal/agent"
)

func TestAgentDeliveryReachesRealStore(t *testing.T) {
	store := openStore(t)
	const code = "0123456789"
	if _, limited, err := store.CreateEnrollmentCode(hashSecret(code), time.Now()); err != nil || limited {
		t.Fatal(err)
	}
	handler := New(store, Options{Loopback: true}).Handler()
	received := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		if r.Method == http.MethodPost && r.URL.Path == "/v1/snapshots" {
			select {
			case received <- struct{}{}:
			default:
			}
		}
	}))
	defer server.Close()
	enrollment, err := agent.Enroll(context.Background(), server.Client(), server.URL, code, "Máquina")
	if err != nil {
		t.Fatal("enrolamento sintético falhou")
	}
	home := t.TempDir()
	if err := agent.Save(agent.PathsFor(home), server.URL, enrollment.Token, nil); err != nil {
		t.Fatal(err)
	}
	opt := agent.CaptureOptions{
		StateDir: filepath.Join(home, "state"),
		Host:     "fixture",
		BootID:   "boot-1",
		Herdr: func(context.Context) ([]byte, error) {
			return []byte(`{"result":{"snapshot":{"workspaces":[],"tabs":[],"panes":[],"layouts":[]}}}`), nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- agent.RunConnected(ctx, opt, home, time.Hour, io.Discard)
	}()
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatal("snapshot não chegou ao servidor")
	}
	if count(t, store, `SELECT COUNT(*) FROM presence`) != 1 ||
		count(t, store, `SELECT COUNT(*) FROM snapshots`) != 1 {
		t.Fatal("presença ou captura não foi persistida")
	}
	const cookie = "synthetic-operator-session"
	if err := store.OpenSession(hashSecret(cookie), "synthetic-csrf", time.Now()); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal("não foi possível consultar a lista")
	}
	html, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(html), "Máquina") || !strings.Contains(string(html), enrollment.DeviceID) {
		t.Fatal("máquina enrolada não apareceu na lista")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("encerramento: %v", err)
	}
}

func TestVerifyDeliveryWithRealSQLiteServer(t *testing.T) {
	store := openStore(t)
	const token = "synthetic-verify-token"
	if err := store.CreateDevice("verify-device", "Máquina", hashSecret(token), time.Now()); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, Options{Loopback: true}).Handler())
	defer server.Close()
	home := t.TempDir()
	if err := agent.Save(agent.PathsFor(home), server.URL, token, nil); err != nil {
		t.Fatal(err)
	}
	opt := agent.CaptureOptions{
		StateDir: filepath.Join(home, "state"),
		Host:     "fixture",
		BootID:   "boot-1",
		Herdr: func(context.Context) ([]byte, error) {
			return []byte(`{"result":{"snapshot":{"workspaces":[],"tabs":[],"panes":[],"layouts":[]}}}`), nil
		},
	}
	if err := agent.VerifyDelivery(context.Background(), home, opt); err != nil {
		t.Fatal(err)
	}
	if count(t, store, `SELECT COUNT(*) FROM presence`) != 1 || count(t, store, `SELECT COUNT(*) FROM snapshots`) != 1 {
		t.Fatal("presença e captura não chegaram ao SQLite")
	}
}

func TestConnectedAgentReportsDiagnosticsToPanel(t *testing.T) {
	store := openStore(t)
	const token = "synthetic-diagnostic-token"
	if err := store.CreateDevice("diagnostic-device", "Máquina", hashSecret(token), time.Now()); err != nil {
		t.Fatal(err)
	}
	handler := New(store, Options{Loopback: true}).Handler()
	reported := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		if r.URL.Path == "/v1/diagnostics" && count(t, store, `SELECT COUNT(*) FROM snapshots`) > 0 {
			select {
			case reported <- struct{}{}:
			default:
			}
		}
	}))
	defer server.Close()
	home := t.TempDir()
	if err := agent.Save(agent.PathsFor(home), server.URL, token, nil); err != nil {
		t.Fatal(err)
	}
	opt := agent.CaptureOptions{StateDir: filepath.Join(home, "state"), Host: "fixture", BootID: "boot-1",
		Herdr: func(context.Context) ([]byte, error) {
			return []byte(`{"result":{"snapshot":{"workspaces":[],"tabs":[],"panes":[],"layouts":[]}}}`), nil
		}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.RunConnected(ctx, opt, home, time.Hour, io.Discard) }()
	select {
	case <-reported:
	case <-ctx.Done():
		t.Fatal("diagnóstico não chegou ao servidor")
	}
	detail, found, err := store.panelDetail("diagnostic-device", "", time.Now())
	if err != nil || !found || !strings.Contains(detail.Diagnostics, "Recente: 0 itens pendentes, 0 em quarentena") {
		t.Fatal("diagnóstico do agente não chegou ao painel")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestConnectedAgentReportsBlockedQueue(t *testing.T) {
	store := openStore(t)
	const token = "synthetic-diagnostic-token"
	if err := store.CreateDevice("blocked-device", "Máquina", hashSecret(token), time.Now()); err != nil {
		t.Fatal(err)
	}
	handler := New(store, Options{Loopback: true}).Handler()
	reported := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/presence" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
		if r.URL.Path == "/v1/diagnostics" {
			select {
			case reported <- struct{}{}:
			default:
			}
		}
	}))
	defer server.Close()
	home := t.TempDir()
	if err := agent.Save(agent.PathsFor(home), server.URL, token, nil); err != nil {
		t.Fatal(err)
	}
	opt := agent.CaptureOptions{StateDir: filepath.Join(home, "state"), Host: "fixture", BootID: "boot-1",
		Herdr: func(context.Context) ([]byte, error) {
			return []byte(`{"result":{"snapshot":{"workspaces":[],"tabs":[],"panes":[],"layouts":[]}}}`), nil
		}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.RunConnected(ctx, opt, home, time.Hour, io.Discard) }()
	select {
	case <-reported:
	case <-ctx.Done():
		t.Fatal("diagnóstico da fila bloqueada não chegou")
	}
	detail, found, err := store.panelDetail("blocked-device", "", time.Now())
	if err != nil || !found || !strings.Contains(detail.Diagnostics, "2 itens pendentes") {
		t.Fatal("fila bloqueada não apareceu no painel")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestConnectedAgentReportsQuarantinedConflict(t *testing.T) {
	store := openStore(t)
	const token = "synthetic-conflict-token"
	if err := store.CreateDevice("conflict-device", "Máquina", hashSecret(token), time.Now()); err != nil {
		t.Fatal(err)
	}
	handler := New(store, Options{Loopback: true}).Handler()
	reported := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/snapshots" {
			w.WriteHeader(http.StatusConflict)
			return
		}
		handler.ServeHTTP(w, r)
		if r.URL.Path == "/v1/diagnostics" && count(t, store, `SELECT COUNT(*) FROM agent_diagnostics WHERE conflict_count=1`) == 1 {
			select {
			case reported <- struct{}{}:
			default:
			}
		}
	}))
	defer server.Close()
	home := t.TempDir()
	if err := agent.Save(agent.PathsFor(home), server.URL, token, nil); err != nil {
		t.Fatal(err)
	}
	opt := agent.CaptureOptions{StateDir: filepath.Join(home, "state"), Host: "fixture", BootID: "boot-1",
		Herdr: func(context.Context) ([]byte, error) {
			return []byte(`{"result":{"snapshot":{"workspaces":[],"tabs":[],"panes":[],"layouts":[]}}}`), nil
		}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.RunConnected(ctx, opt, home, time.Hour, io.Discard) }()
	select {
	case <-reported:
	case <-ctx.Done():
		t.Fatal("conflito não chegou ao diagnóstico remoto")
	}
	detail, found, err := store.panelDetail("conflict-device", "", time.Now())
	if err != nil || !found || !strings.Contains(detail.Diagnostics, "1 em quarentena") {
		t.Fatal("conflito não apareceu no painel")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
