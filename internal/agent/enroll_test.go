package agent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnrollPostsTheCodeOnceAndDoesNotFollowRedirects(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/v1/devices" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Fatal("bearer no enrolamento")
		}
		var body struct {
			Code string `json:"code"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Code != "CODIGO12" || body.Name != "Mesa 1" {
			t.Fatal("corpo")
		}
		http.Redirect(w, r, "/outro", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	_, err := Enroll(t.Context(), server.Client(), server.URL, " CODIGO12 ", "  Mesa 1  ")
	var refused *Refusal
	if !errors.As(err, &refused) || hits != 1 {
		t.Fatalf("status recusado, pedidos %d", hits)
	}
}

func TestEnrollStoresTheTokenOutsideTheConfig(t *testing.T) {
	const token = "token-de-teste"
	const code = "CODIGO12"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"device_id":"dev-1","token":"`+token+`","name":"Máquina"}`)
	}))
	t.Cleanup(server.Close)
	result, err := Enroll(t.Context(), server.Client(), server.URL+"/", code, "Máquina")
	if err != nil || result.Token != token || result.Name != "Máquina" {
		t.Fatal("enrolamento")
	}
	home := t.TempDir()
	paths := PathsFor(home)
	spool := []byte("fila-ses_abcdef12\n")
	if err := os.MkdirAll(filepath.Dir(paths.Spool), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Spool, spool, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(paths, server.URL, result.Token, nil); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), token) || strings.Contains(string(config), code) {
		t.Fatal("segredo na configuração")
	}
	loaded, err := Load(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.API != server.URL || loaded.TokenFile != paths.Token {
		t.Fatal("configuração")
	}
	stored, err := os.ReadFile(paths.Token)
	if err != nil || string(stored) != token+"\n" {
		t.Fatal("token")
	}
	info, err := os.Stat(paths.Token)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("modo do token")
	}
	dir, err := os.Stat(filepath.Dir(paths.Config))
	if err != nil || dir.Mode().Perm() != 0o700 {
		t.Fatal("modo do diretório")
	}
	kept, err := os.ReadFile(paths.Spool)
	if err != nil || string(kept) != string(spool) {
		t.Fatal("fila")
	}
}

func TestEnrollRejectsABadNameBeforeTheRequest(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits++
	}))
	t.Cleanup(server.Close)
	_, err := Enroll(t.Context(), server.Client(), server.URL, "CODIGO12", "mesa/1")
	var refused *Refusal
	if !errors.As(err, &refused) || refused.Field != "name" || hits != 0 {
		t.Fatal("nome")
	}
	_, err = Enroll(t.Context(), server.Client(), "http://user:pass@127.0.0.1:8788", "CODIGO12", "Mesa 1")
	if !errors.Is(err, ErrAPI) || hits != 0 {
		t.Fatal("url")
	}
	_, err = Enroll(t.Context(), server.Client(), server.URL, "curto", "Mesa 1")
	if !errors.As(err, &refused) || refused.Field != "code" || hits != 0 {
		t.Fatal("código")
	}
	_, err = Enroll(t.Context(), server.Client(), "http://127.0.0.1:8788/?q=1", "CODIGO12", "Mesa 1")
	if !errors.Is(err, ErrAPI) || hits != 0 {
		t.Fatal("consulta")
	}
}

func TestEnrollRejectsAnEmptyToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"device_id":"dev-1","token":"","name":"Mesa"}`)
	}))
	t.Cleanup(server.Close)
	_, err := Enroll(t.Context(), server.Client(), server.URL, "CODIGO12", "Mesa 1")
	var refused *Refusal
	if !errors.As(err, &refused) || refused.Code != "INVALID_PARAMS" {
		t.Fatal("token")
	}
}

func TestCanonicalAPICollapsesTheSlash(t *testing.T) {
	left, err := CanonicalAPI("http://127.0.0.1:8788/")
	if err != nil {
		t.Fatal(err)
	}
	right, err := CanonicalAPI("http://127.0.0.1:8788")
	if err != nil || left != right {
		t.Fatal("url")
	}
}
