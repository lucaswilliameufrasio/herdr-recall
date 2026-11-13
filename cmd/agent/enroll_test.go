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

func TestDispatchEnrollReplacesTheTokenAndKeepsTheSpool(t *testing.T) {
	const token = "token-de-teste"
	const code = "CODIGO12"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = ioWrite(w, `{"device_id":"dev-1","token":"`+token+`","name":"Mesa 1"}`)
	}))
	t.Cleanup(server.Close)
	home := t.TempDir()
	paths := agent.PathsFor(home)
	if err := os.MkdirAll(filepath.Dir(paths.Spool), 0o700); err != nil {
		t.Fatal(err)
	}
	spool := []byte("fila-ses_abcdef12\n")
	if err := os.WriteFile(paths.Spool, spool, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.Token), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Token, []byte("token-antigo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := []byte("{\n  \"api\": \"" + server.URL + "\",\n  \"token_file\": \"" + paths.Token + "\"\n}\n")
	if err := os.WriteFile(paths.Config, old, 0o600); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(home, ".config", "systemd", "user", "herdr-recall.service")
	if err := os.MkdirAll(filepath.Dir(unit), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("[Service]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errW bytes.Buffer
	args := []string{"enroll", "--api", server.URL + "/", "--code", code, "--name", " Mesa 1 "}
	codeExit := dispatch(args, nil, &out, &errW, home, nil, server.Client(), "linux")
	if codeExit != 0 {
		t.Fatalf("saída %d", codeExit)
	}
	assertNoSecret(t, out.String()+errW.String(), token, code, "token-antigo", "ses_abcdef12")
	if out.String() != "Máquina enrolada.\nO token anterior foi substituído. A fila local foi conservada.\n" {
		t.Fatal("stdout")
	}
	if !strings.Contains(errW.String(), "herdr-recall service install") {
		t.Fatal("aviso da unidade")
	}
	kept, err := os.ReadFile(paths.Spool)
	if err != nil || string(kept) != string(spool) {
		t.Fatal("fila")
	}
	stored, err := os.ReadFile(paths.Token)
	if err != nil || string(stored) != token+"\n" {
		t.Fatal("token")
	}
	loaded, err := agent.Load(paths.Config)
	if err != nil || loaded.API != server.URL || strings.Contains(string(mustRead(t, paths.Config)), token) {
		t.Fatal("configuração")
	}
}

func TestDispatchSetupAsksAndARefusalKeepsTheOldToken(t *testing.T) {
	const code = "CODIGO12"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = ioWrite(w, `{"error":"ENROLLMENT_USED"}`)
	}))
	t.Cleanup(server.Close)
	home := t.TempDir()
	paths := agent.PathsFor(home)
	if err := os.MkdirAll(filepath.Dir(paths.Token), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Token, []byte("token-antigo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := []byte("{\n  \"api\": \"http://127.0.0.1:1\",\n  \"token_file\": \"" + paths.Token + "\"\n}\n")
	if err := os.WriteFile(paths.Config, config, 0o600); err != nil {
		t.Fatal(err)
	}
	input := server.URL + "\n" + code + "\nMesa 1\n"
	var out, errW bytes.Buffer
	codeExit := dispatch([]string{"setup"}, strings.NewReader(input), &out, &errW, home, nil, server.Client(), "linux")
	if codeExit != 1 {
		t.Fatalf("saída %d", codeExit)
	}
	assertNoSecret(t, out.String()+errW.String(), code, "token-antigo")
	if !strings.Contains(out.String(), "A URL mudou. É preciso um código novo. A fila local fica.") {
		t.Fatal("aviso de url")
	}
	if !strings.Contains(errW.String(), "Este código já foi usado.") {
		t.Fatal("erro")
	}
	stored, err := os.ReadFile(paths.Token)
	if err != nil || string(stored) != "token-antigo\n" {
		t.Fatal("token antigo")
	}
	if string(mustRead(t, paths.Config)) != string(config) {
		t.Fatal("configuração mudou")
	}
}

func TestDispatchCorruptConfigDoesNotCallTheServer(t *testing.T) {
	home := t.TempDir()
	paths := agent.PathsFor(home)
	if err := os.MkdirAll(filepath.Dir(paths.Config), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Token, []byte("token-antigo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Config, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("rede")
		return nil, os.ErrClosed
	})}
	var out, errW bytes.Buffer
	codeExit := dispatch([]string{"enroll", "--api", "http://127.0.0.1:8788", "--code", "CODIGO12", "--name", "Mesa 1"}, nil, &out, &errW, home, nil, client, "linux")
	if codeExit != 1 {
		t.Fatalf("saída %d", codeExit)
	}
	assertNoSecret(t, out.String()+errW.String(), "CODIGO12", "token-antigo")
	if !strings.Contains(errW.String(), "Não foi possível enrolar esta máquina.") {
		t.Fatal("erro")
	}
	if string(mustRead(t, paths.Token)) != "token-antigo\n" || string(mustRead(t, paths.Config)) != "{" {
		t.Fatal("arquivos")
	}
}

func TestDispatchRejectsABadNameWithoutLeavingHome(t *testing.T) {
	home := t.TempDir()
	var out, errW bytes.Buffer
	codeExit := dispatch([]string{"enroll", "--api", "http://127.0.0.1:8788", "--code", "CODIGO12", "--name", "mesa/1"}, nil, &out, &errW, home, nil, nil, "linux")
	if codeExit != 1 || !strings.Contains(errW.String(), "Nome inválido.") {
		t.Fatalf("saída %d", codeExit)
	}
	if _, err := os.Stat(agent.PathsFor(home).Config); !os.IsNotExist(err) {
		t.Fatal("configuração criada")
	}
	codeExit = dispatch([]string{"enroll"}, nil, &out, &errW, home, nil, nil, "linux")
	if codeExit != 2 {
		t.Fatalf("uso %d", codeExit)
	}
}

func TestDispatchLatestStaysLocal(t *testing.T) {
	home := t.TempDir()
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("rede")
		return nil, os.ErrClosed
	})}
	var out, errW bytes.Buffer
	if dispatch(nil, nil, &out, &errW, home, nil, client, "linux") != 0 || out.String() != "herdr-recall\n" {
		t.Fatal("nome")
	}
	out.Reset()
	if dispatch([]string{"latest"}, nil, &out, &errW, home, nil, client, "linux") != 0 {
		t.Fatal("latest")
	}
	if !strings.Contains(out.String(), "Nenhum pane nesta captura.") || errW.String() != "latest panes=0\n" {
		t.Fatal("captura")
	}
}

func TestURLSlashIsNotAChange(t *testing.T) {
	home := t.TempDir()
	token := filepath.Join(home, "device.token")
	if err := os.WriteFile(token, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if urlChanged("http://127.0.0.1:8788/", "http://127.0.0.1:8788", token) {
		t.Fatal("barra")
	}
	if !urlChanged("http://127.0.0.1:1", "http://127.0.0.1:8788", token) {
		t.Fatal("host")
	}
	if urlChanged("http://127.0.0.1:1", "http://127.0.0.1:8788", filepath.Join(home, "ausente")) {
		t.Fatal("sem token")
	}
}

func TestUnitInstalledReadsThePlatformFile(t *testing.T) {
	home := t.TempDir()
	if unitInstalled(home, "linux") || unitInstalled(home, "darwin") {
		t.Fatal("unidade ausente")
	}
	path := filepath.Join(home, "Library", "LaunchAgents", "dev.herdr-recall.agent.plist")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("plist"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !unitInstalled(home, "darwin") || unitInstalled(home, "linux") {
		t.Fatal("plataforma")
	}
}

func assertNoSecret(t *testing.T, text string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatal("segredo no texto")
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func ioWrite(w interface{ Write([]byte) (int, error) }, body string) (int, error) {
	return w.Write([]byte(body))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
