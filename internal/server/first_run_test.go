package server

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"herdr-recall/internal/agent"
)

func TestFirstRunFromOperatorSetupToCapturedDesk(t *testing.T) {
	store := openStore(t)
	server := httptest.NewServer(New(store, Options{Loopback: true}).Handler())
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar
	request := func(method, path string, form url.Values) (int, string) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("requisição de teste falhou")
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, string(body)
	}
	password := "senha-sintetica-segura"
	code, _ := request(http.MethodPost, "/acesso/setup", url.Values{"password": {password}, "password_confirmation": {password}})
	if code != http.StatusCreated {
		t.Fatal("setup do operador falhou")
	}
	code, page := request(http.MethodPost, "/acesso/entrar", url.Values{"password": {password}})
	if code != http.StatusOK {
		t.Fatal("login falhou")
	}
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(page)
	if len(csrf) != 2 {
		t.Fatal("CSRF ausente")
	}
	code, page = request(http.MethodPost, "/maquinas/codigo", url.Values{"csrf": {csrf[1]}})
	if code != http.StatusCreated {
		t.Fatal("código de enrolamento não foi criado")
	}
	enrollmentCode := regexp.MustCompile(`<strong>([A-Z0-9]+)</strong>`).FindStringSubmatch(page)
	if len(enrollmentCode) != 2 {
		t.Fatal("código não apareceu no painel")
	}
	enrolled, err := agent.Enroll(context.Background(), client, server.URL, enrollmentCode[1], "Máquina de teste")
	if err != nil {
		t.Fatal("enrolamento falhou")
	}
	home := t.TempDir()
	if err := agent.Save(agent.PathsFor(home), server.URL, enrolled.Token, nil); err != nil {
		t.Fatal("configuração não foi gravada")
	}
	opt := agent.CaptureOptions{StateDir: filepath.Join(home, "state"), Host: "fixture", BootID: "boot-1",
		Herdr: func(context.Context) ([]byte, error) {
			return []byte(`{"result":{"snapshot":{"workspaces":[],"tabs":[],"panes":[],"layouts":[]}}}`), nil
		}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := agent.VerifyDelivery(ctx, home, opt); err != nil {
		t.Fatal("captura não foi confirmada")
	}
	code, page = request(http.MethodGet, "/maquinas/"+enrolled.DeviceID, nil)
	if code != http.StatusOK || !strings.Contains(page, "Máquina de teste") || !strings.Contains(page, "Nenhum pane nesta captura.") {
		t.Fatal("captura não apareceu no painel")
	}
}
