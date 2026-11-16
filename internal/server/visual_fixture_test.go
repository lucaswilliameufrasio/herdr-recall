package server

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"herdr-recall/internal/contract"
)

func TestVisualFixture(t *testing.T) {
	if os.Getenv("RECALL_VISUAL_FIXTURE") != "1" {
		t.Skip("fixture disabled")
	}
	store := openStore(t)
	now := time.Now().UTC()
	if err := store.OpenSession(hashSecret("fixture-cookie"), "fixture-csrf", now); err != nil {
		t.Fatal(err)
	}
	emptyStore := openStore(t)
	if err := emptyStore.OpenSession(hashSecret("fixture-cookie"), "fixture-csrf", now); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"empty", "zero", "no-session", "down", "down-desk", "offline", "previous", "two-boots", "stable", "revoked", "ambiguous", "error"} {
		if err := store.CreateDevice(id, "Máquina "+id, hashSecret("token-"+id), now); err != nil {
			t.Fatal(err)
		}
	}
	pane := contract.Pane{WorkspaceID: "w1", WorkspaceLabel: "Mesa", TabID: "t1", TabLabel: "Terminal", PaneID: "p1", PaneLabel: "Agente", Cwd: "/home/exemplo/projeto", HerdrSession: "ses_example123", Harness: contract.HarnessOpenCode, SessionID: "ses_example123", ResumeArgv: []string{"opencode", "--session", "ses_example123"}, MatchBasis: contract.MatchHerdrAgentSession, Confidence: contract.ConfidenceHigh}
	for _, id := range []string{"zero", "no-session", "down-desk", "offline", "previous", "two-boots", "stable", "revoked", "ambiguous", "error"} {
		captured := now
		if id == "stable" {
			captured = now.Add(-10 * time.Minute)
		}
		panes := []contract.Pane{pane}
		if id == "zero" {
			panes = []contract.Pane{}
		}
		if id == "no-session" {
			panes[0] = contract.Pane{WorkspaceID: "w1", WorkspaceLabel: "Mesa", TabID: "t1", TabLabel: "Terminal", PaneID: "p1", PaneLabel: "Shell", Cwd: "/home/exemplo/projeto", MatchBasis: contract.MatchNone, Confidence: contract.ConfidenceLow}
		}
		if id == "ambiguous" {
			panes[0].Confidence = contract.ConfidenceLow
			panes[0].AmbiguousWith = []string{"p2"}
			other := panes[0]
			other.PaneID = "p2"
			other.AmbiguousWith = []string{"p1"}
			panes = append(panes, other)
		}
		snap := contract.Snapshot{SnapshotID: "0123456789abcdef", Host: "fixture", BootID: "boot-1", CapturedAt: contract.FormatTime(captured), Panes: panes}
		body, err := contract.MarshalSnapshot(snap)
		if err != nil {
			t.Fatal(err)
		}
		received := now.Add(-time.Minute)
		if id == "previous" || id == "two-boots" {
			received = now.Add(-2 * time.Minute)
		}
		if result, err := store.IngestSnapshot(id, body, received); err != nil || result.Status != StatusOK {
			t.Fatalf("%s snapshot: %+v %v", id, result, err)
		}
	}
	newer := contract.Snapshot{SnapshotID: "fedcba9876543210", Host: "fixture", BootID: "boot-2", CapturedAt: contract.FormatTime(now), Panes: []contract.Pane{pane}}
	newerBody, err := contract.MarshalSnapshot(newer)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := store.IngestSnapshot("two-boots", newerBody, now.Add(-time.Minute)); err != nil || result.Status != StatusOK {
		t.Fatalf("two-boots snapshot: %+v %v", result, err)
	}
	for _, id := range []string{"zero", "no-session", "down", "down-desk", "offline", "previous", "two-boots", "stable", "revoked", "ambiguous", "error"} {
		received := now.Add(-time.Minute)
		boot := "boot-1"
		herdrOK := id != "down" && id != "down-desk"
		if id == "previous" || id == "two-boots" {
			boot = "boot-2"
		}
		if id == "offline" {
			received = now.Add(-5 * time.Minute)
		}
		body, err := contract.MarshalPresence(contract.Presence{BootID: boot, ObservedAt: contract.FormatTime(received), PaneCount: 1, HerdrOK: herdrOK})
		if err != nil {
			t.Fatal(err)
		}
		if result, err := store.IngestPresence(id, body, received); err != nil || result.Status != StatusOK {
			t.Fatalf("%s presence: %+v %v", id, result, err)
		}
	}
	if _, err := store.RevokeDevice("revoked", now); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:18788")
	if err != nil {
		t.Fatal(err)
	}
	handler := New(store, Options{Loopback: true, Now: func() time.Time { return now }}).Handler()
	emptyHandler := New(emptyStore, Options{Loopback: true, Now: func() time.Time { return now }}).Handler()
	done := make(chan struct{}, 1)
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__done" {
			w.WriteHeader(http.StatusNoContent)
			done <- struct{}{}
			return
		}
		if r.URL.Path == "/__inject-error" {
			_, err := store.db.Exec(`UPDATE snapshots SET payload=? WHERE device_id=?`, "invalid-private-data", "error")
			if err != nil {
				http.Error(w, "fixture error", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/maquinas/error", http.StatusSeeOther)
			return
		}
		if r.URL.Path == "/__motion" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<p id="report">Aguardando...</p><iframe title="fixture" width="1280" height="700" src="/maquinas/revoked"></iframe><script>
document.querySelector("iframe").addEventListener("load", function () {
  const frame = this.contentWindow;
  const root = frame.document.documentElement;
  const button = frame.document.querySelector(".button-secondary");
  document.querySelector("#report").textContent =
    "reduce=" + frame.matchMedia("(prefers-reduced-motion: reduce)").matches +
    "; token=" + frame.getComputedStyle(root).getPropertyValue("--duration-fast").trim() +
    "; transição=" + frame.getComputedStyle(button).transitionDuration;
});
</script>`)
			return
		}
		if r.URL.Query().Get("nojs") == "1" {
			w.Header().Set("Content-Security-Policy", "script-src 'none'")
		}
		if r.URL.Path == "/__empty" {
			r.URL.Path = "/"
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "fixture-cookie"})
			emptyHandler.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/__viewport/") {
			id := strings.TrimPrefix(r.URL.Path, "/__viewport/")
			switch id {
			case "empty", "zero", "no-session", "down", "down-desk", "offline", "previous", "two-boots", "stable", "revoked", "ambiguous", "error":
			default:
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<iframe title="fixture" width="1280" height="1200" src="/maquinas/%s"></iframe>`, id)
			return
		}
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "fixture-cookie"})
		mode := r.URL.Query().Get("clipboard")
		if strings.HasPrefix(r.URL.Path, "/maquinas/") && (mode == "fail" || mode == "pending") {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, r)
			for key, values := range recorder.Header() {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(recorder.Code)
			behavior := `Promise.reject(new Error("fixture"))`
			if mode == "pending" {
				behavior = `new Promise(() => {})`
			}
			script := `<script>Object.defineProperty(navigator,"clipboard",{value:{writeText:()=>` + behavior + `}});
document.addEventListener("DOMContentLoaded",()=>{const button=document.querySelector("button[data-argv]");const report=document.createElement("p");report.id="fixture-busy";button.parentElement.append(report);const update=()=>report.textContent="fixture: aria-busy="+button.getAttribute("aria-busy");new MutationObserver(update).observe(button,{attributes:true,attributeFilter:["aria-busy"]});update()})</script></head>`
			_, _ = fmt.Fprint(w, strings.Replace(recorder.Body.String(), "</head>", script, 1))
			return
		}
		handler.ServeHTTP(w, r)
	})}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Close() })
	t.Log("fixture listening on 127.0.0.1:18788")
	select {
	case <-done:
	case <-time.After(10 * time.Minute):
		t.Fatal("fixture timed out")
	}
}
