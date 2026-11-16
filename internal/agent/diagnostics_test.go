package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReportDiagnosticsSendsOnlyCountsWithoutChangingFiles(t *testing.T) {
	dir := t.TempDir()
	opt := testOptions(dir, herdrRaw(sessionPane), time.Now())
	if _, err := Capture(context.Background(), opt); err != nil {
		t.Fatal(err)
	}
	queue := readSpoolFile(t, dir)
	if err := saveSpool(filepath.Join(dir, "spool.conflict"), spoolFile{Items: []spoolItem{queue.Items[1]}}, syncDir); err != nil {
		t.Fatal(err)
	}
	var raw string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/diagnostics" || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer synthetic-token" {
			t.Error("requisição incorreta")
		}
		body, _ := io.ReadAll(r.Body)
		raw = string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	if err := reportDiagnostics(context.Background(), deliveryOptions{StateDir: dir, API: server.URL, Token: "synthetic-token"}); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if json.Unmarshal([]byte(raw), &body) != nil || len(body) != 3 || body["pending_count"] != float64(2) || body["conflict_count"] != float64(1) {
		t.Fatal("relato não contém as contagens esperadas")
	}
	for _, private := range []string{"ses_abcdef12", "/home/project", "synthetic-token"} {
		if strings.Contains(raw, private) {
			t.Fatal("relato expôs dados privados")
		}
	}
	if len(readSpoolFile(t, dir).Items) != 2 {
		t.Fatal("relato alterou a fila")
	}
}
