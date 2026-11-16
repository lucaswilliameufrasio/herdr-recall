package server

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"herdr-recall/internal/agent"
)

func TestServiceDoctorWithRealServerAndRevokedDevice(t *testing.T) {
	store := openStore(t)
	const token = "synthetic-doctor-token"
	if err := store.CreateDevice("doctor-device", "Máquina", hashSecret(token), at(0)); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(New(store, Options{Loopback: true}).Handler())
	defer httpServer.Close()
	home := t.TempDir()
	if err := agent.Save(agent.PathsFor(home), httpServer.URL, token, nil); err != nil {
		t.Fatal(err)
	}
	manager := agent.NewManager("linux", home, filepath.Join(home, "herdr-recall"), "fixture", 1000)
	manager.Runner = nil
	manager.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	find := func(status agent.Status) bool {
		for _, check := range manager.Doctor(context.Background()) {
			if check.Name == "token" && check.Status == status {
				return true
			}
		}
		return false
	}
	if !find(agent.StatusOK) {
		t.Fatal("token válido não foi reconhecido pelo servidor real")
	}
	if found, err := store.RevokeDevice("doctor-device", at(1)); err != nil || !found {
		t.Fatal("não foi possível revogar a fixture")
	}
	if !find(agent.StatusFail) {
		t.Fatal("dispositivo revogado não foi detectado")
	}
}
