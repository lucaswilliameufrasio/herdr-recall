package contract

import (
	"strings"
	"testing"
	"time"
)

func TestEmptyDeskMarshalsAsArray(t *testing.T) {
	raw, err := MarshalSnapshot(Snapshot{
		SnapshotID: "0123456789abcdef",
		Host:       "desk",
		BootID:     "boot-1",
		CapturedAt: "2026-11-13T00:00:00.000000000Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"panes":[]`) {
		t.Fatalf("mesa vazia: %s", raw)
	}
	if strings.Contains(string(raw), `"panes":null`) {
		t.Fatalf("mesa nula: %s", raw)
	}
}

func TestOptionalSlicesStayAbsent(t *testing.T) {
	snap := validSnapshot()
	snap.Panes[0].ResumeArgv = []string{}
	snap.Panes[0].AmbiguousWith = []string{}
	snap.Panes[0].MatchBasis = MatchNone
	snap.Panes[0].Confidence = ConfidenceLow
	snap.Panes[0].Harness = ""
	snap.Panes[0].SessionID = ""
	snap.Panes[0].HerdrSession = ""
	raw, err := MarshalSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "resume_argv") || strings.Contains(text, "ambiguous_with") {
		t.Fatalf("slice vazio saiu no fio: %s", text)
	}
}

func TestTimeKeepsNineDigits(t *testing.T) {
	stamp := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	got := FormatTime(stamp)
	if got != "2026-11-13T00:00:00.000000000Z" {
		t.Fatalf("hora: %s", got)
	}
	if _, ok := ParseTime(got); !ok {
		t.Fatal("a hora canônica foi recusada")
	}
	if _, ok := ParseTime("2026-11-13T00:00:00Z"); ok {
		t.Fatal("hora sem nove dígitos passou")
	}
}

func TestHashIgnoresSnapshotIDAndIncludesTime(t *testing.T) {
	left := validSnapshot()
	right := validSnapshot()
	right.SnapshotID = "fedcba9876543210"
	leftHash, err := SemanticHash(left)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := SemanticHash(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash {
		t.Fatalf("o id entrou no hash: %s %s", leftHash, rightHash)
	}
	right.CapturedAt = "2026-11-13T00:00:01.000000000Z"
	changed, err := SemanticHash(right)
	if err != nil {
		t.Fatal(err)
	}
	if changed == leftHash {
		t.Fatal("a hora não entrou no hash")
	}
}

func TestLayoutRoundTripCoversEveryPane(t *testing.T) {
	snap := validSnapshot()
	snap.Layouts = []Layout{{
		WorkspaceID:     "w1",
		TabID:           "t1",
		WorkspaceNumber: 1,
		TabNumber:       1,
		Root:            &Node{Type: NodePane, PaneID: "p1"},
	}}
	raw, err := MarshalSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"layouts":`) || strings.Contains(string(raw), `"rect"`) {
		t.Fatalf("divisão: %s", raw)
	}
	back, err := ValidateSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Layouts) != 1 || back.Layouts[0].Root.PaneID != "p1" {
		t.Fatalf("volta: %+v", back.Layouts)
	}
	without := validSnapshot()
	plain, err := MarshalSnapshot(without)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "layouts") {
		t.Fatalf("mesa antiga ganhou divisão: %s", plain)
	}
	left, err := SemanticHash(without)
	if err != nil {
		t.Fatal(err)
	}
	right, err := SemanticHash(snap)
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatal("a divisão não entrou no hash")
	}
	snap.Layouts[0].Root = &Node{Type: NodePane, PaneID: "missing"}
	broken, err := MarshalSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateSnapshot(broken); err == nil {
		t.Fatal("pane fora da divisão passou")
	}
}

func TestCwdPassesAndForbiddenKeysFail(t *testing.T) {
	body := `{
		"snapshot_id":"0123456789abcdef",
		"host":"desk",
		"boot_id":"boot-1",
		"captured_at":"2026-11-13T00:00:00.000000000Z",
		"panes":[{
			"workspace_id":"w1","workspace_label":"mesa","tab_id":"t1","tab_label":"aba",
			"pane_id":"p1","pane_label":"pane","cwd":"/home/project",
			"match_basis":"none","confidence":"low"
		}]
	}`
	snap, err := ValidateSnapshot([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Panes[0].Cwd != "/home/project" {
		t.Fatalf("cwd: %s", snap.Panes[0].Cwd)
	}
	for _, key := range []string{`"scroll":[]`, `"prompt":"x"`, `"foreground_cwd":"/tmp"`} {
		dirty := strings.Replace(body, `"cwd":"/home/project"`, key+`,"cwd":"/home/project"`, 1)
		if _, err := ValidateSnapshot([]byte(dirty)); err == nil {
			t.Fatalf("chave proibida passou: %s", key)
		}
	}
}

func TestKindPathDoesNotBecomeSession(t *testing.T) {
	if id, ok := SessionFromAgent("path", "/tmp/session"); ok || id != "" {
		t.Fatalf("path virou sessão: %q %v", id, ok)
	}
	id, ok := SessionFromAgent("id", "ses_abcdef12")
	if !ok || id != "ses_abcdef12" {
		t.Fatalf("id recusado: %q %v", id, ok)
	}
}

func TestArgvAllowlist(t *testing.T) {
	if !AllowArgv("claude", []string{"claude", "--resume", "aaaaaaaa"}) {
		t.Fatal("claude recusado")
	}
	if !AllowArgv("grok", []string{"grok", "--resume", "01ARZ3NDEKTSV4RRFFQ69G5FAV"}) {
		t.Fatal("grok recusado")
	}
	if !AllowArgv("opencode", []string{"opencode", "--session", "ses_abcdef12"}) {
		t.Fatal("opencode recusado")
	}
	if AllowArgv("claude", []string{"/usr/bin/claude", "--resume", "aaaaaaaa"}) {
		t.Fatal("caminho no argv passou")
	}
	if AllowArgv("opencode", []string{"opencode", "--resume", "ses_abcdef12"}) {
		t.Fatal("flag trocada passou")
	}
	if !AllowArgv("claude", []string{"claude", "--resume", "ses.a:b_c-1"}) {
		t.Fatal("ponto e dois-pontos recusados")
	}
	if AllowArgv("claude", []string{"claude", "--resume", "ses a"}) {
		t.Fatal("espaço passou")
	}
	if AllowArgv("claude", []string{"claude", "--resume", "ses'a"}) {
		t.Fatal("aspas passaram")
	}
}

func TestCanonicalDoesNotEscapeHTML(t *testing.T) {
	snap := validSnapshot()
	snap.Panes[0].Cwd = "/tmp/a<b"
	snap.Panes[0].MatchBasis = MatchNone
	snap.Panes[0].Confidence = ConfidenceLow
	snap.Panes[0].Harness = ""
	snap.Panes[0].SessionID = ""
	snap.Panes[0].HerdrSession = ""
	snap.Panes[0].ResumeArgv = nil
	raw, err := MarshalSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `/tmp/a<b`) {
		t.Fatalf("cwd escapado: %s", raw)
	}
}

func validSnapshot() Snapshot {
	return Snapshot{
		SnapshotID: "0123456789abcdef",
		Host:       "desk",
		BootID:     "boot-1",
		CapturedAt: "2026-11-13T00:00:00.000000000Z",
		Panes: []Pane{{
			WorkspaceID:    "w1",
			WorkspaceLabel: "mesa",
			TabID:          "t1",
			TabLabel:       "aba",
			PaneID:         "p1",
			PaneLabel:      "pane",
			Cwd:            "/home/project",
			HerdrSession:   "ses_abcdef12",
			Harness:        HarnessOpenCode,
			SessionID:      "ses_abcdef12",
			ResumeArgv:     []string{"opencode", "--session", "ses_abcdef12"},
			MatchBasis:     MatchHerdrAgentSession,
			Confidence:     ConfidenceHigh,
		}},
	}
}
