package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"herdr-recall/internal/contract"
)

const testPassword = "senha-secreta-12"

type clock struct {
	at time.Time
}

type harness struct {
	store  *Store
	server *Server
	clock  *clock
}

func newHarness(t *testing.T, opt Options) *harness {
	t.Helper()
	store := openStore(t)
	clk := &clock{at: at(10)}
	if opt.Now == nil {
		opt.Now = func() time.Time { return clk.at }
	}
	return &harness{store: store, server: New(store, opt), clock: clk}
}

func (h *harness) call(method string, path string, body []byte, header http.Header) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	for key, values := range header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	recorder := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func jsonHeader(extra http.Header) http.Header {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	for key, values := range extra {
		for _, value := range values {
			header.Add(key, value)
		}
	}
	return header
}

func cookieHeader(raw string) http.Header {
	header := http.Header{}
	header.Add("Cookie", sessionCookie+"="+raw)
	return header
}

func bearerHeader(token string) http.Header {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	return header
}

type apiError struct {
	Error  string `json:"error"`
	Fields []struct {
		Field   string `json:"field"`
		Message string `json:"message"`
	} `json:"fields"`
}

func mustCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) apiError {
	t.Helper()
	var body apiError
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != status || body.Error != code {
		t.Fatalf("status %d error %s", rec.Code, body.Error)
	}
	return body
}

func mustJSON(t *testing.T, rec *httptest.ResponseRecorder, status int, dest any) {
	t.Helper()
	if rec.Code != status {
		var body apiError
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		t.Fatalf("status %d error %s", rec.Code, body.Error)
	}
	if dest == nil {
		return
	}
	if err := json.Unmarshal(rec.Body.Bytes(), dest); err != nil {
		t.Fatal("json")
	}
}

func assertNoSecret(t *testing.T, rec *httptest.ResponseRecorder, secret string) {
	t.Helper()
	if secret != "" && strings.Contains(rec.Body.String(), secret) {
		t.Fatal("segredo voltou na resposta")
	}
}

func sessionCookieValue(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	line := rec.Header().Get("Set-Cookie")
	prefix := sessionCookie + "="
	if !strings.HasPrefix(line, prefix) {
		t.Fatal("cookie ausente")
	}
	value := line[len(prefix):]
	if cut := strings.IndexByte(value, ';'); cut >= 0 {
		value = value[:cut]
	}
	if value == "" {
		t.Fatal("cookie vazio")
	}
	return value
}

func assertCookieFlags(t *testing.T, rec *httptest.ResponseRecorder, secure bool) {
	t.Helper()
	line := strings.ToLower(rec.Header().Get("Set-Cookie"))
	for _, part := range []string{"httponly", "samesite=strict", "path=/", "max-age=43200"} {
		if !strings.Contains(line, part) {
			t.Fatalf("cookie sem %s", part)
		}
	}
	if strings.Contains(line, "secure") != secure {
		t.Fatalf("secure=%v", secure)
	}
}

func (h *harness) setupOperator(t *testing.T) {
	t.Helper()
	body := []byte(`{"password":"` + testPassword + `"}`)
	rec := h.call(http.MethodPost, "/acesso/setup", body, jsonHeader(nil))
	mustJSON(t, rec, statusCreated, nil)
	assertNoSecret(t, rec, testPassword)
}

func (h *harness) login(t *testing.T) (string, string) {
	t.Helper()
	body := []byte(`{"password":"` + testPassword + `"}`)
	rec := h.call(http.MethodPost, "/acesso/entrar", body, jsonHeader(nil))
	var payload struct {
		CSRF string `json:"csrf"`
	}
	mustJSON(t, rec, http.StatusOK, &payload)
	assertNoSecret(t, rec, testPassword)
	if payload.CSRF == "" {
		t.Fatal("csrf ausente")
	}
	return sessionCookieValue(t, rec), payload.CSRF
}

func (h *harness) operatorHeader(cookie string, csrf string) http.Header {
	header := cookieHeader(cookie)
	if csrf != "" {
		header.Set("X-CSRF-Token", csrf)
	}
	return header
}

func (h *harness) issueCode(t *testing.T, cookie string, csrf string) (string, string) {
	t.Helper()
	rec := h.call(http.MethodPost, "/maquinas/codigo", nil, h.operatorHeader(cookie, csrf))
	var payload struct {
		Code      string `json:"code"`
		ExpiresAt string `json:"expires_at"`
	}
	mustJSON(t, rec, statusCreated, &payload)
	if len(payload.Code) != enrollmentLength {
		t.Fatalf("tamanho %d", len(payload.Code))
	}
	for _, char := range payload.Code {
		if !strings.ContainsRune(crockfordAlphabet, char) {
			t.Fatal("alfabeto")
		}
	}
	if _, ok := contract.ParseTime(payload.ExpiresAt); !ok {
		t.Fatal("expires_at")
	}
	want := stamp(h.clock.at.Add(enrollmentLife))
	if payload.ExpiresAt != want {
		t.Fatal("validade")
	}
	return payload.Code, payload.ExpiresAt
}

func (h *harness) enroll(t *testing.T, code string, name string) (string, string, string) {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"code": code, "name": name})
	if err != nil {
		t.Fatal(err)
	}
	rec := h.call(http.MethodPost, "/v1/devices", raw, jsonHeader(nil))
	var payload struct {
		DeviceID string `json:"device_id"`
		Token    string `json:"token"`
		Name     string `json:"name"`
	}
	mustJSON(t, rec, statusCreated, &payload)
	if payload.DeviceID == "" || payload.Token == "" {
		t.Fatal("enrolamento incompleto")
	}
	if strings.Contains(rec.Body.String(), hashSecret(payload.Token)) {
		t.Fatal("hash do token na resposta")
	}
	h.assertHash(t, `SELECT token_hash FROM devices WHERE id = ?`, payload.DeviceID, payload.Token)
	return payload.DeviceID, payload.Token, payload.Name
}

func (h *harness) assertHash(t *testing.T, query string, id string, secret string) {
	t.Helper()
	var stored string
	if err := h.store.db.QueryRow(query, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == secret || stored != hashSecret(secret) {
		t.Fatal("hash guardou o segredo")
	}
}

func (h *harness) usedAt(t *testing.T, code string) sql.NullString {
	t.Helper()
	var used sql.NullString
	err := h.store.db.QueryRow(`SELECT used_at FROM enrollment_codes WHERE code_hash = ?`, hashSecret(code)).Scan(&used)
	if err != nil {
		t.Fatal(err)
	}
	return used
}

func TestHealthIgnoresTheDatabase(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	if err := h.store.Close(); err != nil {
		t.Fatal(err)
	}
	rec := h.call(http.MethodGet, "/healthz", nil, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestSetupUsesTheBindNotTheClient(t *testing.T) {
	off := newHarness(t, Options{})
	header := http.Header{}
	header.Set("X-Forwarded-For", "127.0.0.1")
	rec := off.call(http.MethodPost, "/acesso/setup", []byte(`{"password":"`+testPassword+`"}`), jsonHeader(header))
	mustCode(t, rec, statusForbidden, codeForbidden)
	if count(t, off.store, `SELECT COUNT(*) FROM operator`) != 0 {
		t.Fatal("setup público criou operador")
	}

	token := "setup-token-de-teste"
	gated := newHarness(t, Options{SetupToken: token, Loopback: true})
	rec = gated.call(http.MethodGet, "/acesso/setup", nil, nil)
	mustCode(t, rec, statusForbidden, codeForbidden)
	if strings.Contains(rec.Body.String(), "<form") {
		t.Fatal("formulário sem o token")
	}
	wrong := http.Header{}
	wrong.Set("X-Setup-Token", "outro-token-de-teste")
	rec = gated.call(http.MethodPost, "/acesso/setup", []byte(`{"password":"`+testPassword+`"}`), jsonHeader(wrong))
	mustCode(t, rec, statusForbidden, codeForbidden)

	right := http.Header{}
	right.Set("X-Setup-Token", token)
	rec = gated.call(http.MethodGet, "/acesso/setup", nil, right)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "value=") {
		t.Fatalf("status %d", rec.Code)
	}
	rec = gated.call(http.MethodPost, "/acesso/setup", []byte(`{"password":"`+testPassword+`"}`), jsonHeader(right))
	mustJSON(t, rec, statusCreated, nil)
	assertNoSecret(t, rec, testPassword)
	var stored string
	if err := gated.store.db.QueryRow(`SELECT password_hash FROM operator WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, "$12$") || strings.Contains(stored, testPassword) {
		t.Fatal("bcrypt")
	}
}

func TestSetupPasswordAndSecondOperator(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	form := []byte("password=" + url.QueryEscape(testPassword))
	header := http.Header{}
	header.Set("Content-Type", "application/x-www-form-urlencoded")
	page := h.call(http.MethodGet, "/acesso/setup", nil, nil)
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), "value=") {
		t.Fatalf("status %d", page.Code)
	}
	short := h.call(http.MethodPost, "/acesso/setup", []byte(`{"password":"curta"}`), jsonHeader(nil))
	body := mustCode(t, short, StatusUnprocessable, codeInvalidParams)
	if len(body.Fields) != 1 || body.Fields[0].Field != "password" {
		t.Fatal("campo password")
	}
	if count(t, h.store, `SELECT COUNT(*) FROM operator`) != 0 {
		t.Fatal("senha curta criou operador")
	}
	rec := h.call(http.MethodPost, "/acesso/setup", form, header)
	mustJSON(t, rec, statusCreated, nil)
	assertNoSecret(t, rec, testPassword)
	again := h.call(http.MethodGet, "/acesso/setup", nil, nil)
	mustCode(t, again, statusNotFound, codeNotFound)
	again = h.call(http.MethodPost, "/acesso/setup", []byte(`{"password":"`+testPassword+`"}`), jsonHeader(nil))
	mustCode(t, again, statusNotFound, codeNotFound)
	if count(t, h.store, `SELECT COUNT(*) FROM operator`) != 1 {
		t.Fatal("segundo operador")
	}
}

func TestLoginCookieDoesNotRenew(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	missing := h.call(http.MethodPost, "/acesso/entrar", []byte(`{"password":"`+testPassword+`"}`), jsonHeader(nil))
	mustCode(t, missing, statusUnauthorized, codeUnauthorized)
	h.setupOperator(t)
	bad := h.call(http.MethodPost, "/acesso/entrar", []byte(`{"password":"senha-errada-12"}`), jsonHeader(nil))
	mustCode(t, bad, statusUnauthorized, codeUnauthorized)
	if bad.Header().Get("Set-Cookie") != "" {
		t.Fatal("cookie em falha")
	}
	cookie, csrf := h.login(t)
	loginRec := h.call(http.MethodPost, "/acesso/entrar", []byte(`{"password":"`+testPassword+`"}`), jsonHeader(nil))
	assertCookieFlags(t, loginRec, false)
	h.assertHash(t, `SELECT token_hash FROM sessions WHERE csrf = ?`, csrf, cookie)

	var expires string
	if err := h.store.db.QueryRow(`SELECT expires_at FROM sessions WHERE token_hash = ?`, hashSecret(cookie)).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires != stamp(h.clock.at.Add(sessionLife)) {
		t.Fatal("validade da sessão")
	}
	home := h.call(http.MethodGet, "/", nil, cookieHeader(cookie))
	if home.Code != http.StatusOK || home.Body.String() != "ok" {
		t.Fatalf("status %d", home.Code)
	}
	var after string
	if err := h.store.db.QueryRow(`SELECT expires_at FROM sessions WHERE token_hash = ?`, hashSecret(cookie)).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != expires {
		t.Fatal("a sessão foi renovada")
	}
	anon := h.call(http.MethodGet, "/", nil, nil)
	mustCode(t, anon, statusUnauthorized, codeUnauthorized)

	h.clock.at = h.clock.at.Add(sessionLife + time.Second)
	stale := h.call(http.MethodGet, "/", nil, cookieHeader(cookie))
	mustCode(t, stale, statusUnauthorized, codeUnauthorized)

	secure := newHarness(t, Options{Loopback: true, TLS: true})
	secure.setupOperator(t)
	secureLogin := secure.call(http.MethodPost, "/acesso/entrar", []byte(`{"password":"`+testPassword+`"}`), jsonHeader(nil))
	mustJSON(t, secureLogin, http.StatusOK, nil)
	assertCookieFlags(t, secureLogin, true)
}

func TestDeviceTokenCannotOpenHome(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	h.setupOperator(t)
	cookie, csrf := h.login(t)
	code, _ := h.issueCode(t, cookie, csrf)
	id, token, name := h.enroll(t, code, "  Máquina  ")
	if name != "Máquina" {
		t.Fatal("nome")
	}
	home := h.call(http.MethodGet, "/", nil, bearerHeader(token))
	mustCode(t, home, statusUnauthorized, codeUnauthorized)
	if strings.Contains(home.Body.String(), name) {
		t.Fatal("a home listou a máquina")
	}
	operatorHome := h.call(http.MethodGet, "/", nil, cookieHeader(cookie))
	if operatorHome.Code != http.StatusOK || strings.Contains(operatorHome.Body.String(), name) {
		t.Fatal("a home listou a máquina")
	}
	detail := h.call(http.MethodGet, "/maquinas/"+id, nil, bearerHeader(token))
	mustCode(t, detail, statusUnauthorized, codeUnauthorized)
	detail = h.call(http.MethodGet, "/maquinas/"+id, nil, cookieHeader(cookie))
	var machine map[string]any
	mustJSON(t, detail, http.StatusOK, &machine)
	if machine["name"] != name || machine["panes"] != nil || machine["resume_argv"] != nil {
		t.Fatal("detalhe")
	}
}

func TestEnrollmentLimitAndExchange(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	h.setupOperator(t)
	cookie, csrf := h.login(t)
	denied := h.call(http.MethodPost, "/maquinas/codigo", nil, cookieHeader(cookie))
	mustCode(t, denied, statusForbidden, codeForbidden)
	if count(t, h.store, `SELECT COUNT(*) FROM enrollment_codes`) != 0 {
		t.Fatal("código sem csrf")
	}

	var codes []string
	for i := 0; i < enrollmentCap; i++ {
		code, _ := h.issueCode(t, cookie, csrf)
		codes = append(codes, code)
		var stored string
		if err := h.store.db.QueryRow(`SELECT code_hash FROM enrollment_codes WHERE code_hash = ?`, hashSecret(code)).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored == code {
			t.Fatal("código em claro")
		}
	}
	limited := h.call(http.MethodPost, "/maquinas/codigo", nil, h.operatorHeader(cookie, csrf))
	mustCode(t, limited, statusTooMany, codeRateLimited)
	if count(t, h.store, `SELECT COUNT(*) FROM enrollment_codes`) != enrollmentCap {
		t.Fatal("o sexto código foi gravado")
	}

	id, token, _ := h.enroll(t, codes[0], "Mesa 1")
	device := h.call(http.MethodGet, "/v1/device", nil, bearerHeader(token))
	var shown map[string]string
	mustJSON(t, device, http.StatusOK, &shown)
	if shown["id"] != id || shown["name"] != "Mesa 1" || shown["created_at"] != stamp(h.clock.at) || len(shown) != 3 {
		t.Fatal("dispositivo")
	}
	if lastSeen(t, h.store, id) != "" || count(t, h.store, `SELECT COUNT(*) FROM presence`) != 0 {
		t.Fatal("GET /v1/device gravou")
	}
	reused := h.call(http.MethodPost, "/v1/devices", []byte(`{"code":"`+codes[0]+`","name":"Mesa 1"}`), jsonHeader(nil))
	mustCode(t, reused, statusUnauthorized, codeEnrollmentUsed)
	if count(t, h.store, `SELECT COUNT(*) FROM devices`) != 1 {
		t.Fatal("código usado criou outra máquina")
	}

	badName := h.call(http.MethodPost, "/v1/devices", []byte(`{"code":"`+codes[1]+`","name":"mesa/1"}`), jsonHeader(nil))
	fields := mustCode(t, badName, StatusUnprocessable, codeInvalidParams)
	if len(fields.Fields) != 1 || fields.Fields[0].Field != "name" {
		t.Fatal("campo name")
	}
	if h.usedAt(t, codes[1]).Valid {
		t.Fatal("nome inválido consumiu o código")
	}
	_, _, storedName := h.enroll(t, codes[1], "  Mesa 1  ")
	if storedName != "Mesa 1" {
		t.Fatal("trim")
	}

	short := h.call(http.MethodPost, "/v1/devices", []byte(`{"code":"curto","name":"Mesa 1"}`), jsonHeader(nil))
	shortFields := mustCode(t, short, StatusUnprocessable, codeInvalidParams)
	if len(shortFields.Fields) != 1 || shortFields.Fields[0].Field != "code" {
		t.Fatal("campo code")
	}
	if h.usedAt(t, codes[2]).Valid {
		t.Fatal("código malformado consumiu outro")
	}

	mutated := "A" + codes[3][1:]
	if mutated == codes[3] {
		mutated = "B" + codes[3][1:]
	}
	lower := h.call(http.MethodPost, "/v1/devices", []byte(`{"code":"`+mutated+`","name":"Mesa 1"}`), jsonHeader(nil))
	mustCode(t, lower, statusUnauthorized, codeEnrollmentInvalid)
	if h.usedAt(t, codes[3]).Valid {
		t.Fatal("código desconhecido consumiu o original")
	}
	h.enroll(t, codes[3], "Mesa 1")

	h.clock.at = h.clock.at.Add(11 * time.Minute)
	expired := h.call(http.MethodPost, "/v1/devices", []byte(`{"code":"`+codes[4]+`","name":"Mesa 1"}`), jsonHeader(nil))
	mustCode(t, expired, statusUnauthorized, codeEnrollmentExpired)
	if h.usedAt(t, codes[4]).Valid {
		t.Fatal("código expirado foi consumido")
	}

	h.clock.at = at(10).Add(61 * time.Minute)
	fresh, _ := h.issueCode(t, cookie, csrf)
	if fresh == "" {
		t.Fatal("código novo")
	}
	if count(t, h.store, `SELECT COUNT(*) FROM enrollment_codes`) != enrollmentCap+1 {
		t.Fatal("código antigo foi apagado")
	}
}

func TestRevocationKeepsTheDesk(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	h.setupOperator(t)
	cookie, csrf := h.login(t)
	code, _ := h.issueCode(t, cookie, csrf)
	id, token, _ := h.enroll(t, code, "Mesa 1")
	snapshot := desk(snapID(1), bootID(1), stampAt(1))
	ok := h.call(http.MethodPost, "/v1/snapshots", snapshot, jsonHeader(bearerHeader(token)))
	if ok.Code != StatusOK {
		mustCode(t, ok, StatusOK, "")
	}
	again := h.call(http.MethodPost, "/v1/snapshots", snapshot, jsonHeader(bearerHeader(token)))
	if again.Code != StatusOK {
		mustCode(t, again, StatusOK, "")
	}
	payload := readDesk(t, h.store, id, bootID(1)).payload
	conflict := h.call(http.MethodPost, "/v1/snapshots", deskChanged(snapID(1), bootID(1), stampAt(1)), jsonHeader(bearerHeader(token)))
	mustCode(t, conflict, StatusConflict, CodeSnapshotConflict)
	if readDesk(t, h.store, id, bootID(1)).payload != payload {
		t.Fatal("conflito cobriu a mesa")
	}
	presence := presenceBody(bootID(1), stampAt(1), 0, true)
	seen := h.call(http.MethodPost, "/v1/presence", presence, jsonHeader(bearerHeader(token)))
	if seen.Code != StatusOK {
		mustCode(t, seen, StatusOK, "")
	}
	seenAgain := h.call(http.MethodPost, "/v1/presence", presence, jsonHeader(bearerHeader(token)))
	if seenAgain.Code != StatusOK || count(t, h.store, `SELECT COUNT(*) FROM presence`) != 1 {
		t.Fatal("presença repetida")
	}
	before := lastSeen(t, h.store, id)
	quiet := h.call(http.MethodGet, "/v1/device", nil, bearerHeader(token))
	if quiet.Code != http.StatusOK || lastSeen(t, h.store, id) != before {
		t.Fatal("GET /v1/device moveu last_seen_at")
	}

	missing := h.call(http.MethodPost, "/v1/snapshots", snapshot, nil)
	mustCode(t, missing, statusUnauthorized, codeUnauthorized)
	cookieOnly := h.call(http.MethodPost, "/v1/snapshots", snapshot, jsonHeader(cookieHeader(cookie)))
	mustCode(t, cookieOnly, statusUnauthorized, codeUnauthorized)
	unknown := h.call(http.MethodPost, "/v1/snapshots", snapshot, jsonHeader(bearerHeader("token-desconhecido")))
	mustCode(t, unknown, statusUnauthorized, codeUnauthorized)

	noCSRF := h.call(http.MethodPost, "/maquinas/"+id+"/revogar", nil, cookieHeader(cookie))
	mustCode(t, noCSRF, statusForbidden, codeForbidden)
	if count(t, h.store, `SELECT COUNT(*) FROM devices WHERE revoked_at IS NOT NULL`) != 0 {
		t.Fatal("revogou sem csrf")
	}
	revoked := h.call(http.MethodPost, "/maquinas/"+id+"/revogar", nil, h.operatorHeader(cookie, csrf))
	if revoked.Code != http.StatusOK {
		mustCode(t, revoked, http.StatusOK, "")
	}
	var revokedAt string
	if err := h.store.db.QueryRow(`SELECT revoked_at FROM devices WHERE id = ?`, id).Scan(&revokedAt); err != nil {
		t.Fatal(err)
	}
	if revokedAt != stamp(h.clock.at) {
		t.Fatal("revoked_at")
	}
	h.clock.at = h.clock.at.Add(time.Minute)
	second := h.call(http.MethodPost, "/maquinas/"+id+"/revogar", nil, h.operatorHeader(cookie, csrf))
	if second.Code != http.StatusOK {
		mustCode(t, second, http.StatusOK, "")
	}
	var still string
	if err := h.store.db.QueryRow(`SELECT revoked_at FROM devices WHERE id = ?`, id).Scan(&still); err != nil {
		t.Fatal(err)
	}
	if still != revokedAt {
		t.Fatal("revogação moveu o carimbo")
	}
	blocked := h.call(http.MethodPost, "/v1/snapshots", deskChanged(snapID(2), bootID(1), stampAt(2)), jsonHeader(bearerHeader(token)))
	mustCode(t, blocked, statusUnauthorized, codeDeviceRevoked)
	if readDesk(t, h.store, id, bootID(1)).payload != payload || count(t, h.store, `SELECT COUNT(*) FROM snapshots`) != 1 {
		t.Fatal("revogação apagou ou cobriu a mesa")
	}
	blockedPresence := h.call(http.MethodPost, "/v1/presence", presenceBody(bootID(1), stampAt(2), 1, false), jsonHeader(bearerHeader(token)))
	mustCode(t, blockedPresence, statusUnauthorized, codeDeviceRevoked)
	if count(t, h.store, `SELECT COUNT(*) FROM presence`) != 1 {
		t.Fatal("revogação gravou presença")
	}
	hidden := h.call(http.MethodGet, "/v1/device", nil, bearerHeader(token))
	mustCode(t, hidden, statusUnauthorized, codeDeviceRevoked)
	stillThere := h.call(http.MethodGet, "/maquinas/"+id, nil, cookieHeader(cookie))
	var machine map[string]string
	mustJSON(t, stillThere, http.StatusOK, &machine)
	if machine["name"] != "Mesa 1" {
		t.Fatal("a máquina sumiu")
	}
	absent := h.call(http.MethodPost, "/maquinas/nao-existe/revogar", nil, h.operatorHeader(cookie, csrf))
	mustCode(t, absent, statusNotFound, codeNotFound)
}

func TestIngestRejectsABadBody(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	code, err := enrollmentCode()
	if err != nil {
		t.Fatal(err)
	}
	if _, limited, err := h.store.CreateEnrollmentCode(hashSecret(code), h.clock.at); err != nil || limited {
		t.Fatal(err)
	}
	_, token, _ := h.enroll(t, code, "Mesa 1")
	bad := h.call(http.MethodPost, "/v1/snapshots", []byte(`{"snapshot_id":"0123456789abcdef","foreground_cwd":"/tmp"}`), jsonHeader(bearerHeader(token)))
	mustCode(t, bad, StatusUnprocessable, codeInvalidParams)
	huge := bytes.Repeat([]byte("a"), snapshotMax+1)
	over := h.call(http.MethodPost, "/v1/snapshots", huge, jsonHeader(bearerHeader(token)))
	mustCode(t, over, statusPayload, codePayloadTooLarge)
	if count(t, h.store, `SELECT COUNT(*) FROM snapshots`) != 0 || count(t, h.store, `SELECT COUNT(*) FROM snapshot_receipts`) != 0 {
		t.Fatal("corpo inválido foi escrito")
	}
}

func TestLogoutRequiresCSRF(t *testing.T) {
	h := newHarness(t, Options{Loopback: true})
	h.setupOperator(t)
	cookie, csrf := h.login(t)
	kept := h.call(http.MethodPost, "/acesso/sair", nil, cookieHeader(cookie))
	mustCode(t, kept, statusForbidden, codeForbidden)
	if count(t, h.store, `SELECT COUNT(*) FROM sessions`) != 1 {
		t.Fatal("saiu sem csrf")
	}
	left := h.call(http.MethodPost, "/acesso/sair", nil, h.operatorHeader(cookie, csrf))
	if left.Code != http.StatusOK {
		mustCode(t, left, http.StatusOK, "")
	}
	if count(t, h.store, `SELECT COUNT(*) FROM sessions`) != 0 {
		t.Fatal("sessão ficou")
	}
	line := strings.ToLower(left.Header().Get("Set-Cookie"))
	if !strings.Contains(line, "max-age=0") && !strings.Contains(line, "max-age=-1") {
		t.Fatal("cookie não foi apagado")
	}
	after := h.call(http.MethodGet, "/", nil, cookieHeader(cookie))
	mustCode(t, after, statusUnauthorized, codeUnauthorized)
}

func TestListenRefusesOffLoopbackWithoutTLS(t *testing.T) {
	h := newHarness(t, Options{})
	if err := h.server.Listen("0.0.0.0:8788"); err == nil {
		t.Fatal("subiu")
	}
	if err := refuseBind("192.0.2.10:8788", false); err == nil {
		t.Fatal("endereço público")
	}
	if err := refuseBind("0.0.0.0:8788", true); err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"127.0.0.1:8788", "[::1]:8788", "localhost:8788"} {
		if !loopbackAddr(addr) {
			t.Fatalf("loopback %s", addr)
		}
	}
	if loopbackAddr("0.0.0.0:8788") {
		t.Fatal("0.0.0.0")
	}
}
