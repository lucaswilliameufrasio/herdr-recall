package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const TimeLayout = "2006-01-02T15:04:05.000000000Z"

const (
	MatchHerdrAgentSession = "herdr_agent_session"
	MatchNone              = "none"
	ConfidenceHigh         = "high"
	ConfidenceLow          = "low"
	HarnessClaude          = "claude"
	HarnessGrok            = "grok"
	HarnessOpenCode        = "opencode"
	maxPanes               = 128
	maxLabelRunes          = 80
	maxIDRunes             = 80
	maxCwdBytes            = 1024
	maxHostBytes           = 253
	maxSessionRunes        = 128
)

type Pane struct {
	WorkspaceID    string   `json:"workspace_id"`
	WorkspaceLabel string   `json:"workspace_label"`
	TabID          string   `json:"tab_id"`
	TabLabel       string   `json:"tab_label"`
	PaneID         string   `json:"pane_id"`
	PaneLabel      string   `json:"pane_label"`
	Cwd            string   `json:"cwd"`
	HerdrSession   string   `json:"herdr_session,omitempty"`
	Harness        string   `json:"harness,omitempty"`
	SessionID      string   `json:"session_id,omitempty"`
	ResumeArgv     []string `json:"resume_argv,omitempty"`
	MatchBasis     string   `json:"match_basis"`
	Confidence     string   `json:"confidence"`
	AmbiguousWith  []string `json:"ambiguous_with,omitempty"`
}

type Snapshot struct {
	SnapshotID string `json:"snapshot_id"`
	Host       string `json:"host"`
	BootID     string `json:"boot_id"`
	CapturedAt string `json:"captured_at"`
	Panes      []Pane `json:"panes"`
}

type Presence struct {
	BootID     string `json:"boot_id"`
	ObservedAt string `json:"observed_at"`
	PaneCount  int    `json:"pane_count"`
	HerdrOK    bool   `json:"herdr_ok"`
}

type semantic struct {
	BootID     string `json:"boot_id"`
	CapturedAt string `json:"captured_at"`
	Host       string `json:"host"`
	Panes      []Pane `json:"panes"`
}

type FieldError struct {
	Field   string
	Message string
}

type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	return "validação"
}

func FormatTime(t time.Time) string {
	return t.UTC().Format(TimeLayout)
}

func ParseTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(TimeLayout, value)
	if err != nil {
		return time.Time{}, false
	}
	if parsed.UTC().Format(TimeLayout) != value {
		return time.Time{}, false
	}
	return parsed, true
}

func SessionFromAgent(kind string, value string) (string, bool) {
	if kind != "id" {
		return "", false
	}
	if !sessionOK(value) {
		return "", false
	}
	return value, true
}

func AllowArgv(harness string, argv []string) bool {
	if len(argv) != 3 {
		return false
	}
	switch harness {
	case HarnessClaude, HarnessGrok:
		return argv[0] == harness && argv[1] == "--resume" && sessionOK(argv[2])
	case HarnessOpenCode:
		return argv[0] == HarnessOpenCode && argv[1] == "--session" && sessionOK(argv[2])
	default:
		return false
	}
}

func ValidateSnapshot(raw []byte) (Snapshot, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var snap Snapshot
	if err := decoder.Decode(&snap); err != nil {
		return Snapshot{}, fieldError("body", "json inválido")
	}
	extra, err := io.ReadAll(decoder.Buffered())
	if err != nil {
		return Snapshot{}, fieldError("body", "json inválido")
	}
	if strings.TrimSpace(string(extra)) != "" {
		return Snapshot{}, fieldError("body", "json inválido")
	}
	if !panesPresent(raw) {
		return Snapshot{}, fieldError("panes", "a mesa é um array")
	}
	if err := checkSnapshot(snap); err != nil {
		return Snapshot{}, err
	}
	return normalize(snap), nil
}

func MarshalSnapshot(snap Snapshot) ([]byte, error) {
	return encode(normalize(snap))
}

func SemanticHash(snap Snapshot) (string, error) {
	normalized := normalize(snap)
	body := semantic{
		BootID:     normalized.BootID,
		CapturedAt: normalized.CapturedAt,
		Host:       normalized.Host,
		Panes:      normalized.Panes,
	}
	raw, err := encode(body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func ValidatePresence(raw []byte) (Presence, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var presence Presence
	if err := decoder.Decode(&presence); err != nil {
		return Presence{}, fieldError("body", "json inválido")
	}
	extra, err := io.ReadAll(decoder.Buffered())
	if err != nil {
		return Presence{}, fieldError("body", "json inválido")
	}
	if strings.TrimSpace(string(extra)) != "" {
		return Presence{}, fieldError("body", "json inválido")
	}
	if !hasJSONKey(raw, "herdr_ok") || !hasJSONKey(raw, "pane_count") {
		return Presence{}, fieldError("body", "campo obrigatório ausente")
	}
	if !bootOK(presence.BootID) {
		return Presence{}, fieldError("boot_id", "boot inválido")
	}
	if _, ok := ParseTime(presence.ObservedAt); !ok {
		return Presence{}, fieldError("observed_at", "hora inválida")
	}
	if presence.PaneCount < 0 || presence.PaneCount > maxPanes {
		return Presence{}, fieldError("pane_count", "fora do limite")
	}
	return presence, nil
}

func MarshalPresence(presence Presence) ([]byte, error) {
	return encode(presence)
}

func normalize(snap Snapshot) Snapshot {
	if snap.Panes == nil {
		snap.Panes = []Pane{}
	}
	panes := make([]Pane, len(snap.Panes))
	for i, pane := range snap.Panes {
		if len(pane.ResumeArgv) == 0 {
			pane.ResumeArgv = nil
		}
		if len(pane.AmbiguousWith) == 0 {
			pane.AmbiguousWith = nil
		}
		panes[i] = pane
	}
	slices.SortFunc(panes, func(left Pane, right Pane) int {
		return strings.Compare(left.PaneID, right.PaneID)
	})
	snap.Panes = panes
	return snap
}

func checkSnapshot(snap Snapshot) error {
	if !snapshotIDOK(snap.SnapshotID) {
		return fieldError("snapshot_id", "id inválido")
	}
	if !hostOK(snap.Host) {
		return fieldError("host", "host inválido")
	}
	if !bootOK(snap.BootID) {
		return fieldError("boot_id", "boot inválido")
	}
	if _, ok := ParseTime(snap.CapturedAt); !ok {
		return fieldError("captured_at", "hora inválida")
	}
	if len(snap.Panes) > maxPanes {
		return fieldError("panes", "fora do limite")
	}
	seenPane := map[string]struct{}{}
	sessions := map[string][]string{}
	for _, pane := range snap.Panes {
		if err := checkPane(pane); err != nil {
			return err
		}
		if _, exists := seenPane[pane.PaneID]; exists {
			return fieldError("pane_id", "pane repetido")
		}
		seenPane[pane.PaneID] = struct{}{}
		if pane.SessionID != "" {
			sessions[pane.SessionID] = append(sessions[pane.SessionID], pane.PaneID)
		}
	}
	for _, pane := range snap.Panes {
		others := otherPanes(sessions[pane.SessionID], pane.PaneID)
		if pane.SessionID == "" {
			if len(pane.AmbiguousWith) != 0 {
				return fieldError("ambiguous_with", "pane sem sessão")
			}
			continue
		}
		if !sameIDs(pane.AmbiguousWith, others) {
			return fieldError("ambiguous_with", "não lista os outros panes da sessão")
		}
		if len(others) > 0 && pane.Confidence != ConfidenceLow {
			return fieldError("confidence", "sessão repetida é baixa")
		}
	}
	return nil
}

func checkPane(pane Pane) error {
	if !idOK(pane.WorkspaceID) || !idOK(pane.TabID) || !idOK(pane.PaneID) {
		return fieldError("pane_id", "id inválido")
	}
	if !labelOK(pane.WorkspaceLabel) || !labelOK(pane.TabLabel) || !labelOK(pane.PaneLabel) {
		return fieldError("pane_label", "rótulo longo")
	}
	if !cwdOK(pane.Cwd) {
		return fieldError("cwd", "caminho inválido")
	}
	switch pane.MatchBasis {
	case MatchNone:
		if pane.Harness != "" || pane.SessionID != "" || pane.HerdrSession != "" || len(pane.ResumeArgv) != 0 {
			return fieldError("match_basis", "pane sem sessão não leva comando")
		}
		if pane.Confidence != ConfidenceLow {
			return fieldError("confidence", "pane sem sessão é baixa")
		}
	case MatchHerdrAgentSession:
		if !sessionOK(pane.SessionID) || pane.HerdrSession != pane.SessionID {
			return fieldError("session_id", "sessão inválida")
		}
		if !AllowArgv(pane.Harness, pane.ResumeArgv) || pane.ResumeArgv[2] != pane.SessionID {
			return fieldError("resume_argv", "comando recusado")
		}
		if len(pane.AmbiguousWith) == 0 && pane.Confidence != ConfidenceHigh {
			return fieldError("confidence", "sessão única é alta")
		}
	default:
		return fieldError("match_basis", "base inválida")
	}
	return nil
}

func otherPanes(ids []string, self string) []string {
	others := make([]string, 0)
	for _, id := range ids {
		if id == self {
			continue
		}
		others = append(others, id)
	}
	slices.Sort(others)
	return others
}

func sameIDs(got []string, want []string) bool {
	copied := append([]string{}, got...)
	slices.Sort(copied)
	if len(copied) != len(want) {
		return false
	}
	for i := range copied {
		if copied[i] != want[i] {
			return false
		}
	}
	return true
}

func sessionOK(value string) bool {
	count := utf8.RuneCountInString(value)
	if count < 1 || count > maxSessionRunes {
		return false
	}
	for _, r := range value {
		if r > unicode.MaxASCII {
			return false
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '.', '_', ':', '-':
			continue
		default:
			return false
		}
	}
	return true
}

func snapshotIDOK(value string) bool {
	if len(value) < 16 || len(value) > 80 {
		return false
	}
	for _, r := range value {
		if r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_') {
			return false
		}
	}
	return true
}

func bootOK(value string) bool {
	if value == "" || utf8.RuneCountInString(value) > maxIDRunes {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == ' ' {
			return false
		}
	}
	return true
}

func idOK(value string) bool {
	if value == "" || utf8.RuneCountInString(value) > maxIDRunes {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func labelOK(value string) bool {
	return utf8.RuneCountInString(value) <= maxLabelRunes
}

func hostOK(value string) bool {
	if value == "" || len(value) > maxHostBytes {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func cwdOK(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > maxCwdBytes || !strings.HasPrefix(value, "/") {
		return false
	}
	if strings.ContainsAny(value, "\r\n") {
		return false
	}
	return true
}

func panesPresent(raw []byte) bool {
	var probe struct {
		Panes json.RawMessage `json:"panes"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	trimmed := bytes.TrimSpace(probe.Panes)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	return trimmed[0] == '['
}

func hasJSONKey(raw []byte, key string) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return false
	}
	_, ok := object[key]
	return ok
}

func encode(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func fieldError(field string, message string) error {
	return &ValidationError{Fields: []FieldError{{Field: field, Message: message}}}
}
