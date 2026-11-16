package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"herdr-recall/internal/agent"
)

func TestDispatchRunRejectsExtraArguments(t *testing.T) {
	var out, errW bytes.Buffer
	code := dispatch([]string{"run", "agora"}, nil, &out, &errW, t.TempDir(), nil, nil, "linux")
	if code != 2 || !strings.Contains(errW.String(), "uso: herdr-recall run") {
		t.Fatalf("code=%d err=%q", code, errW.String())
	}
}

func TestServiceDoctorPrintsRecoveryWithoutToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	home := t.TempDir()
	const secret = "synthetic-revoked-token"
	if err := agent.Save(agent.PathsFor(home), server.URL, secret, nil); err != nil {
		t.Fatal(err)
	}
	manager := agent.NewManager("linux", home, filepath.Join(home, "herdr-recall"), "fixture", 1000)
	manager.Runner = nil
	manager.LookPath = func(string) (string, error) { return "/usr/bin/systemctl", nil }
	var out, errW bytes.Buffer
	code := serviceDoctor(nil, &out, &errW, manager)
	if code != 1 || !strings.Contains(out.String(), "token recusado ou dispositivo revogado") || !strings.Contains(out.String(), "herdr-recall setup") {
		t.Fatalf("doctor não indicou recuperação: code=%d", code)
	}
	if strings.Contains(out.String(), secret) || strings.Contains(errW.String(), secret) {
		t.Fatal("token foi exibido")
	}
}

func TestServiceInstallDryRunRequiresEnrollmentAndDoesNotWrite(t *testing.T) {
	home := t.TempDir()
	manager := agent.NewManager("linux", home, filepath.Join(home, "herdr-recall"), "fixture", 1000)
	var out, errW bytes.Buffer
	if code := serviceInstall([]string{"--dry-run"}, &out, &errW, manager); code != 1 {
		t.Fatal("dry-run sem enrolamento não foi recusado")
	}
	const token = "synthetic-dryrun-token"
	if err := agent.Save(agent.PathsFor(home), "http://127.0.0.1:12345", token, nil); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errW.Reset()
	if code := serviceInstall([]string{"--dry-run"}, &out, &errW, manager); code != 0 || !strings.Contains(out.String(), "Nada foi instalado.") {
		t.Fatalf("dry-run falhou: code=%d", code)
	}
	unit, err := manager.FilePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Fatal("dry-run gravou unidade")
	}
	if strings.Contains(out.String(), token) || strings.Contains(errW.String(), token) {
		t.Fatal("dry-run expôs token")
	}
}

func TestDispatchServiceUsageAndWindows(t *testing.T) {
	home := t.TempDir()
	var out, errW bytes.Buffer
	code := dispatch([]string{"service"}, nil, &out, &errW, home, nil, nil, "linux")
	if code != 2 || !strings.Contains(errW.String(), "uso: herdr-recall service") {
		t.Fatalf("code=%d err=%q", code, errW.String())
	}
	out.Reset()
	errW.Reset()
	code = dispatch([]string{"service", "install"}, nil, &out, &errW, home, nil, nil, "windows")
	if code != 1 || !strings.Contains(errW.String(), "Windows") {
		t.Fatalf("code=%d err=%q", code, errW.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", "herdr-recall.service")); !os.IsNotExist(err) {
		t.Fatal("o windows gravou uma unidade")
	}
}
