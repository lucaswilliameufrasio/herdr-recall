package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerStartsWithPrivateDatabaseAndShutsDown(t *testing.T) {
	home := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listening := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, nil, home, "", io.Discard, func(network, addr string) (net.Listener, error) {
			close(listening)
			return listener, nil
		})
	}()
	<-listening
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatal("healthz indisponível")
	}
	path := filepath.Join(home, ".local", "state", "herdr-recall-server", "server.db")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("banco não foi criado com modo privado")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("servidor não encerrou: %v", err)
	}
}

func TestServerRejectsPublicBindBeforeOpeningDatabase(t *testing.T) {
	home := t.TempDir()
	called := false
	err := run(context.Background(), []string{"--bind", "0.0.0.0:8788"}, home, "", io.Discard, func(string, string) (net.Listener, error) {
		called = true
		return nil, nil
	})
	if err == nil || called {
		t.Fatal("bind público foi aceito")
	}
	if _, err := os.Stat(filepath.Join(home, ".local")); !os.IsNotExist(err) {
		t.Fatal("bind público tocou o diretório de dados")
	}
}

func TestDatabaseRefusesInsecureExistingFileWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareDatabase(path); err == nil {
		t.Fatal("banco acessível por outros usuários foi aceito")
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "existing" {
		t.Fatal("banco existente foi alterado")
	}
}

func TestDatabaseRefusesSymlinkWithoutChangingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "other")
	if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "server.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := prepareDatabase(link); err == nil {
		t.Fatal("symlink foi aceito")
	}
	body, err := os.ReadFile(target)
	if err != nil || string(body) != "existing" {
		t.Fatal("arquivo de destino foi alterado")
	}
}
