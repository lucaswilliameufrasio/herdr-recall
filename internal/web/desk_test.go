package web

import (
	"bytes"
	"strings"
	"testing"

	"herdr-recall/internal/contract"
)

func TestRenderDeskEscapesDataAndRequiresAllowedCommand(t *testing.T) {
	snapshot := contract.Snapshot{Panes: []contract.Pane{
		{TabLabel: `<script>alert(1)</script>`, Harness: "opencode", SessionID: "ses_123", ResumeArgv: []string{"opencode", "--session", "ses_123"}, Confidence: contract.ConfidenceHigh},
		{Harness: "opencode", SessionID: "ses_456", ResumeArgv: []string{"opencode", "--session", "ses_123"}, Confidence: contract.ConfidenceLow},
	}}
	var out bytes.Buffer
	if err := RenderDetailPage(&out, "csrf", Detail{HasSnapshot: true, Panes: DeskPanes(snapshot)}); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{"&lt;script&gt;", "<details>", "&#39;opencode&#39;", "data-argv=", "alta", "baixa", "Sem sessão para restaurar."} {
		if !strings.Contains(html, want) {
			t.Errorf("texto ausente: %s", want)
		}
	}
	if strings.Contains(html, "<script>") || strings.Count(html, "data-argv=") != 1 {
		t.Fatal("conteúdo inseguro ou comando inválido")
	}
}
