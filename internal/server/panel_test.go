package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"herdr-recall/internal/contract"
)

func TestPanelRendersStoredDeskAndPresence(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	h.setupOperator(t)
	cookie, csrf := h.login(t)
	code, _ := h.issueCode(t, cookie, csrf)
	id, token, _ := h.enroll(t, code, "Mesa 1")
	snapshot := contract.Snapshot{
		SnapshotID: snapID(1), Host: "host", BootID: "boot-1", CapturedAt: stampAt(3),
		Panes: []contract.Pane{{
			WorkspaceID: "w1", WorkspaceLabel: "mesa", TabID: "t1", TabLabel: "<aba>",
			PaneID: "p1", PaneLabel: "pane", Cwd: "/home/projeto",
			HerdrSession: "ses_abcdef12", Harness: contract.HarnessOpenCode, SessionID: "ses_abcdef12",
			ResumeArgv: []string{"opencode", "--session", "ses_abcdef12"},
			MatchBasis: contract.MatchHerdrAgentSession, Confidence: contract.ConfidenceHigh,
		}},
	}
	payload, err := contract.MarshalSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := h.store.IngestSnapshot(id, payload, at(4)); err != nil || outcome.Status != StatusOK {
		t.Fatalf("captura: %+v %v", outcome, err)
	}
	for _, minute := range []int{1, 3} {
		if outcome, err := h.store.IngestPresence(id, presenceBody("boot-1", stampAt(minute), 1, true), at(minute+4)); err != nil || outcome.Status != StatusOK {
			t.Fatalf("presença: %+v %v", outcome, err)
		}
	}
	header := cookieHeader(cookie)
	home := h.call(http.MethodGet, "/", nil, header)
	for _, want := range []string{"Mesa 1", "boot-1", "1 panes", "/maquinas/" + id} {
		if home.Code != http.StatusOK || !strings.Contains(home.Body.String(), want) {
			t.Errorf("lista sem %q: %d", want, home.Code)
		}
	}
	detail := h.call(http.MethodGet, "/maquinas/"+id, nil, header)
	for _, want := range []string{"&lt;aba&gt;", "/home/projeto", "ses_abcdef12", `data-argv=`, "<details>", "2 de 15.", "presence-title"} {
		if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), want) {
			t.Errorf("detalhe sem %q: %d", want, detail.Code)
		}
	}
	if strings.Contains(detail.Body.String(), "<aba>") || strings.Contains(detail.Body.String(), token) || strings.Count(detail.Body.String(), "<rect ") != 15 {
		t.Fatal("escape, token ou faixa inválida")
	}
	if detail.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cache")
	}
	asset := h.call(http.MethodGet, "/assets/panel.js", nil, nil)
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "navigator.clipboard.writeText") {
		t.Fatal("script ausente")
	}
	denied := h.call(http.MethodGet, "/maquinas/"+id, nil, bearerHeader(token))
	mustCode(t, denied, statusUnauthorized, codeUnauthorized)
	missing := h.call(http.MethodGet, "/maquinas/"+id+"?boot=inexistente", nil, header)
	mustCode(t, missing, statusNotFound, codeNotFound)
}

func TestLoginFormReportsFailureAndRedirectsOnSuccess(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	h.setupOperator(t)
	header := http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}}
	failed := h.call(http.MethodPost, "/acesso/entrar", []byte("password=senha-errada"), header)
	if failed.Code != http.StatusUnauthorized || !strings.Contains(failed.Body.String(), "Senha não confere.") || !strings.Contains(failed.Body.String(), `aria-invalid="true"`) {
		t.Fatalf("erro do formulário: %d", failed.Code)
	}
	success := h.call(http.MethodPost, "/acesso/entrar", []byte("password="+url.QueryEscape(testPassword)), header)
	if success.Code != http.StatusSeeOther || success.Header().Get("Location") != "/" || success.Header().Get("Set-Cookie") == "" {
		t.Fatalf("login: %d %s", success.Code, success.Header().Get("Location"))
	}
}

func TestSetupFormConfirmsPasswordAndOffersLogin(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	header := http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}}
	page := h.call(http.MethodGet, "/acesso/setup", nil, nil)
	if !strings.Contains(page.Body.String(), `name="password_confirmation"`) {
		t.Fatal("confirmação ausente")
	}
	mismatch := h.call(http.MethodPost, "/acesso/setup", []byte("password="+url.QueryEscape(testPassword)+"&password_confirmation=outra"), header)
	if mismatch.Code != StatusUnprocessable || !strings.Contains(mismatch.Body.String(), "As senhas não conferem.") || !strings.Contains(mismatch.Body.String(), `aria-invalid="true"`) || count(t, h.store, `SELECT COUNT(*) FROM operator`) != 0 {
		t.Fatal("confirmação divergente foi aceita")
	}
	body := "password=" + url.QueryEscape(testPassword) + "&password_confirmation=" + url.QueryEscape(testPassword)
	created := h.call(http.MethodPost, "/acesso/setup", []byte(body), header)
	if created.Code != statusCreated || !strings.Contains(created.Body.String(), `href="/acesso/entrar"`) || strings.Contains(created.Body.String(), testPassword) {
		t.Fatal("jornada de setup incompleta")
	}
}

func TestEmptyPanelCanGenerateEnrollmentCodeWithCSRF(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	h.setupOperator(t)
	cookie, csrf := h.login(t)
	header := cookieHeader(cookie)
	home := h.call(http.MethodGet, "/", nil, header)
	if !strings.Contains(home.Body.String(), `action="/maquinas/codigo"`) || !strings.Contains(home.Body.String(), `value="`+csrf+`"`) {
		t.Fatal("formulário de código ausente")
	}
	header.Set("Content-Type", "application/x-www-form-urlencoded")
	denied := h.call(http.MethodPost, "/maquinas/codigo", []byte("csrf=errado"), header)
	mustCode(t, denied, statusForbidden, codeForbidden)
	response := h.call(http.MethodPost, "/maquinas/codigo", []byte("csrf="+url.QueryEscape(csrf)), header)
	if response.Code != statusCreated || !strings.Contains(response.Header().Get("Content-Type"), "text/html") || !strings.Contains(response.Body.String(), "Código de enrolamento") || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("código no painel: %d", response.Code)
	}
	if count(t, h.store, `SELECT COUNT(*) FROM enrollment_codes`) != 1 {
		t.Fatal("quantidade de códigos incorreta")
	}
}

func TestPanelPresenceUsesObservedTimeOfLatestReceivedRow(t *testing.T) {
	store := openStore(t)
	mustDevice(t, store, "dev-1", at(0))
	for _, entry := range []struct{ observed, received int }{{3, 4}, {1, 5}} {
		outcome, err := store.IngestPresence("dev-1", presenceBody("boot-1", stampAt(entry.observed), 0, true), at(entry.received))
		if err != nil || outcome.Status != StatusOK {
			t.Fatalf("presença: %+v %v", outcome, err)
		}
	}
	detail, found, err := store.panelDetail("dev-1", "", at(5))
	if err != nil || !found {
		t.Fatalf("detalhe: %v, %v", found, err)
	}
	if !strings.Contains(detail.Presence.Label, "00:01 UTC: 1 de 15") {
		t.Fatalf("âncora incorreta: %s", detail.Presence.Label)
	}
}

func TestPanelDatabaseReadFailureShowsSafeError(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	h.setupOperator(t)
	cookie, csrf := h.login(t)
	code, _ := h.issueCode(t, cookie, csrf)
	id, _, _ := h.enroll(t, code, "Mesa")
	if outcome, err := h.store.IngestSnapshot(id, desk(snapID(1), "boot-1", stampAt(1)), at(2)); err != nil || outcome.Status != StatusOK {
		t.Fatalf("snapshot: %+v %v", outcome, err)
	}
	if _, err := h.store.db.Exec(`UPDATE snapshots SET payload=? WHERE device_id=?`, "invalid-private-data", id); err != nil {
		t.Fatal(err)
	}
	response := h.call(http.MethodGet, "/maquinas/"+id, nil, cookieHeader(cookie))
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "Não foi possível mostrar as capturas.") || strings.Contains(response.Body.String(), "invalid-private-data") {
		t.Fatalf("erro inseguro: status %d", response.Code)
	}
}

func TestPanelExistingEmptySnapshotAndPaneWithoutSession(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	h.setupOperator(t)
	cookie, _ := h.login(t)
	mustDevice(t, h.store, "zero", at(0))
	mustDevice(t, h.store, "no-session", at(0))
	snapshots := map[string]contract.Snapshot{
		"zero": {
			SnapshotID: snapID(1), Host: "host", BootID: "boot-1", CapturedAt: stampAt(1), Panes: []contract.Pane{},
		},
		"no-session": {
			SnapshotID: snapID(2), Host: "host", BootID: "boot-1", CapturedAt: stampAt(1),
			Panes: []contract.Pane{{WorkspaceID: "w1", TabID: "t1", PaneID: "p1", PaneLabel: "Shell", Cwd: "/home/exemplo", MatchBasis: contract.MatchNone, Confidence: contract.ConfidenceLow}},
		},
	}
	for id, snapshot := range snapshots {
		raw, err := contract.MarshalSnapshot(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if result, err := h.store.IngestSnapshot(id, raw, at(2)); err != nil || result.Status != StatusOK {
			t.Fatalf("%s: %+v %v", id, result, err)
		}
	}
	empty := h.call(http.MethodGet, "/maquinas/zero", nil, cookieHeader(cookie))
	if empty.Code != http.StatusOK || !strings.Contains(empty.Body.String(), "Nenhum pane nesta captura.") || strings.Contains(empty.Body.String(), "<table") {
		t.Fatal("captura vazia não foi apresentada corretamente")
	}
	noSession := h.call(http.MethodGet, "/maquinas/no-session", nil, cookieHeader(cookie))
	if noSession.Code != http.StatusOK || !strings.Contains(noSession.Body.String(), "Sem sessão para restaurar.") || strings.Contains(noSession.Body.String(), "data-argv=") || strings.Contains(noSession.Body.String(), "<details>") {
		t.Fatal("pane sem sessão ofereceu ação ou omitiu mensagem")
	}
}
