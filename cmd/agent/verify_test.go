package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"herdr-recall/internal/agent"
)

func TestVerifyRejectsInvalidArgumentsWithoutWriting(t *testing.T) {
	var out, errW bytes.Buffer
	if code := dispatch([]string{"verify", "--invalid"}, nil, &out, &errW, t.TempDir(), nil, nil, "linux"); code != 2 {
		t.Fatalf("exit: %d", code)
	}
	if !strings.Contains(errW.String(), "verify") {
		t.Fatal("uso ausente")
	}
}

func TestVerifySendUsesCaptureAndFIFO(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s' '{\"result\":{\"snapshot\":{\"workspaces\":[],\"tabs\":[],\"panes\":[],\"layouts\":[]}}}'\n"
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := agent.Save(agent.PathsFor(home), server.URL, "synthetic-token", nil); err != nil {
		t.Fatal(err)
	}
	var out, errW bytes.Buffer
	if code := verifyCommand(true, &out, &errW, home, "linux"); code != 0 {
		t.Fatalf("verify: %d %s", code, errW.String())
	}
	if strings.Join(paths, ",") != "/v1/presence,/v1/snapshots" || !strings.Contains(out.String(), "Painel e restauração não foram verificados") {
		t.Fatal("jornada ou limites não foram apresentados")
	}
	if strings.Contains(out.String(), "synthetic-token") || strings.Contains(errW.String(), "synthetic-token") {
		t.Fatal("credencial exposta")
	}
}

func TestVerifySendConflictReturnsFailure(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s' '{\"result\":{\"snapshot\":{\"workspaces\":[],\"tabs\":[],\"panes\":[],\"layouts\":[]}}}'\n"
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()
	if err := agent.Save(agent.PathsFor(home), server.URL, "synthetic-token", nil); err != nil {
		t.Fatal(err)
	}
	var out, errW bytes.Buffer
	if code := verifyCommand(true, &out, &errW, home, "linux"); code == 0 {
		t.Fatal("conflito retornou sucesso")
	}
	if !strings.Contains(errW.String(), "quarentena") || strings.Contains(out.String(), "confirmada pelo servidor") {
		t.Fatal("resultado de conflito incorreto")
	}
}

func TestVerifyDefaultDoesNotClaimDelivery(t *testing.T) {
	var out, errW bytes.Buffer
	verifyCommand(false, &out, &errW, t.TempDir(), "linux")
	if !strings.Contains(out.String(), "não comprova entrega") || !strings.Contains(out.String(), "verify --send") {
		t.Fatal("limites ou instrução ausentes")
	}
}
