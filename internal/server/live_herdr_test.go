package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"herdr-recall/internal/agent"
	"herdr-recall/internal/contract"
)

// TestLiveHerdrDelivery is opt-in because it reads the user's real Herdr desk.
// Its database and agent state live exclusively in test-owned temporary paths.
func TestLiveHerdrDelivery(t *testing.T) {
	if os.Getenv("HERDR_RECALL_LIVE_TEST") != "1" {
		t.Skip("real Herdr verification is opt-in")
	}
	store := openStore(t)
	const token = "synthetic-live-test-token"
	if err := store.CreateDevice("live-fixture", "Máquina de teste", hashSecret(token), time.Now()); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, Options{Loopback: true}).Handler())
	defer server.Close()
	home := t.TempDir()
	if err := agent.Save(agent.PathsFor(home), server.URL, token, nil); err != nil {
		t.Fatal("não foi possível preparar a configuração de teste")
	}
	opt := agent.CaptureOptions{
		StateDir: filepath.Join(home, "state"),
		Host:     "live-test",
		BootID:   "live-test-boot",
		Herdr:    agent.RunHerdrSnapshot,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := agent.VerifyDelivery(ctx, home, opt); err != nil {
		t.Fatal("captura ou envio real não foi confirmado")
	}
	var payload string
	if err := store.db.QueryRow(`SELECT payload FROM snapshots WHERE device_id=?`, "live-fixture").Scan(&payload); err != nil {
		t.Fatal("captura não chegou ao banco isolado")
	}
	snapshot, err := contract.ValidateSnapshot([]byte(payload))
	if err != nil || len(snapshot.Panes) == 0 {
		t.Fatal("captura recebida é inválida ou não contém panes")
	}
	workspaces := make(map[string]bool)
	for _, pane := range snapshot.Panes {
		workspaces[pane.WorkspaceID] = true
	}
	if err := store.OpenSession(hashSecret("synthetic-live-session"), "synthetic-csrf", time.Now()); err != nil {
		t.Fatal("não foi possível preparar a sessão de teste")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/maquinas/live-fixture", nil)
	if err != nil {
		t.Fatal("não foi possível preparar a consulta de teste")
	}
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "synthetic-live-session"})
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal("painel de teste indisponível")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("painel não apresentou a captura")
	}
	detail, found, err := store.panelDetail("live-fixture", "", time.Now())
	if err != nil || !found || len(detail.Panes) != len(snapshot.Panes) {
		t.Fatal("dados reais não chegaram ao detalhe do painel")
	}
	for _, pane := range snapshot.Panes {
		if strings.TrimSpace(pane.WorkspaceID) == "" {
			t.Fatal("captura contém workspace sem identificação")
		}
	}
	t.Logf("captura real recebida: %d workspaces com panes, %d panes", len(workspaces), len(snapshot.Panes))
}
