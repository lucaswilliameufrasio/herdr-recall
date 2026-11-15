package server

import (
	"net/http"
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
