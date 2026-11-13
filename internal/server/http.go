package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"herdr-recall/internal/contract"

	"golang.org/x/crypto/bcrypt"
)

const (
	snapshotMax = 256 << 10
	smallMax    = 4 << 10
)

const setupForm = `<!DOCTYPE html>
<meta charset="utf-8">
<form method="post" action="/acesso/setup">
<label>Senha <input name="password" type="password" autocomplete="new-password"></label>
<button type="submit">Criar</button>
</form>
`

const loginForm = `<!DOCTYPE html>
<meta charset="utf-8">
<form method="post" action="/acesso/entrar">
<label>Senha <input name="password" type="password" autocomplete="current-password"></label>
<button type="submit">Entrar</button>
</form>
`

// Options configures one process. Loopback is the bind, not the client.
type Options struct {
	Now        func() time.Time
	SetupToken string
	Loopback   bool
	TLS        bool
}

// Server is the first listener. It does not render the desk.
type Server struct {
	store      *Store
	nowFn      func() time.Time
	setupToken string
	loopback   bool
	tls        bool
	handler    http.Handler
}

// New builds the routes. It does not bind a port.
func New(store *Store, opt Options) *Server {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	server := &Server{
		store:      store,
		nowFn:      opt.Now,
		setupToken: opt.SetupToken,
		loopback:   opt.Loopback,
		tls:        opt.TLS,
	}
	server.handler = server.routes()
	return server
}

// Handler is the API used by tests and by Listen.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Listen refuses a public address when TLS is off. It does not trust proxy headers.
func (s *Server) Listen(addr string) error {
	if err := refuseBind(addr, s.tls); err != nil {
		return err
	}
	s.loopback = loopbackAddr(addr)
	if s.tls {
		return fmt.Errorf("TLS exige certificado")
	}
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return httpServer.ListenAndServe()
}

func refuseBind(addr string, tlsOn bool) error {
	if tlsOn || loopbackAddr(addr) {
		return nil
	}
	return fmt.Errorf("fora de loopback sem TLS")
}

func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func (s *Server) now() time.Time {
	return s.nowFn()
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /acesso/setup", s.setupGet)
	mux.HandleFunc("POST /acesso/setup", s.setupPost)
	mux.HandleFunc("GET /acesso/entrar", s.loginGet)
	mux.HandleFunc("POST /acesso/entrar", s.loginPost)
	mux.HandleFunc("POST /acesso/sair", s.logout)
	mux.HandleFunc("GET /", s.home)
	mux.HandleFunc("POST /maquinas/codigo", s.createCode)
	mux.HandleFunc("GET /maquinas/{id}", s.machine)
	mux.HandleFunc("POST /maquinas/{id}/revogar", s.revoke)
	mux.HandleFunc("POST /v1/devices", s.enroll)
	mux.HandleFunc("POST /v1/snapshots", s.snapshot)
	mux.HandleFunc("POST /v1/presence", s.presence)
	mux.HandleFunc("GET /v1/device", s.showDevice)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func (s *Server) setupGet(w http.ResponseWriter, r *http.Request) {
	if !s.allowSetup(w, r) {
		return
	}
	_, exists, err := s.store.OperatorHash()
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	if exists {
		writeError(w, statusNotFound, codeNotFound, nil)
		return
	}
	writeHTML(w, http.StatusOK, setupForm)
}

func (s *Server) setupPost(w http.ResponseWriter, r *http.Request) {
	if !s.allowSetup(w, r) {
		return
	}
	password, ok := s.readPassword(w, r)
	if !ok {
		return
	}
	if message, bad := passwordProblem(password); bad {
		writeError(w, StatusUnprocessable, codeInvalidParams, []contract.FieldError{{
			Field:   "password",
			Message: message,
		}})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	created, err := s.store.CreateOperator(string(hash), s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	if !created {
		writeError(w, statusNotFound, codeNotFound, nil)
		return
	}
	w.WriteHeader(statusCreated)
}

func (s *Server) loginGet(w http.ResponseWriter, _ *http.Request) {
	writeHTML(w, http.StatusOK, loginForm)
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	password, ok := s.readPassword(w, r)
	if !ok {
		return
	}
	hash, exists, err := s.store.OperatorHash()
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	var match bool
	if exists {
		match = bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	} else {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash(), []byte(password))
	}
	if !match {
		writeError(w, statusUnauthorized, codeUnauthorized, nil)
		return
	}
	raw, err := randomHex(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	csrf, err := randomHex(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	if err := s.store.OpenSession(hashSecret(raw), csrf, s.now()); err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	s.setCookie(w, raw, sessionMaxAge)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"csrf": csrf})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	hash, csrf, ok := s.operator(w, r)
	if !ok {
		return
	}
	if !s.requireCSRF(w, r, csrf) {
		return
	}
	if err := s.store.DeleteSession(hash); err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	s.setCookie(w, "", -1)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.operator(w, r); !ok {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func (s *Server) createCode(w http.ResponseWriter, r *http.Request) {
	_, csrf, ok := s.operator(w, r)
	if !ok {
		return
	}
	if !s.requireCSRF(w, r, csrf) {
		return
	}
	raw, err := enrollmentCode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	expires, limited, err := s.store.CreateEnrollmentCode(hashSecret(raw), s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	if limited {
		writeError(w, statusTooMany, codeRateLimited, nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, statusCreated, map[string]string{
		"code":       raw,
		"expires_at": expires,
	})
}

func (s *Server) machine(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.operator(w, r); !ok {
		return
	}
	row, found, err := s.store.DeviceByID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	if !found {
		writeError(w, statusNotFound, codeNotFound, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"id":   row.ID,
		"name": row.Name,
	})
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	_, csrf, ok := s.operator(w, r)
	if !ok {
		return
	}
	if !s.requireCSRF(w, r, csrf) {
		return
	}
	found, err := s.store.RevokeDevice(r.PathValue("id"), s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	if !found {
		writeError(w, statusNotFound, codeNotFound, nil)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	raw, ok := readLimited(w, r, smallMax)
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := decodeObject(raw, &body); err != nil {
		writeError(w, StatusUnprocessable, codeInvalidParams, []contract.FieldError{{
			Field:   "body",
			Message: "json inválido",
		}})
		return
	}
	var fields []contract.FieldError
	if len(body.Code) < 8 || len(body.Code) > 120 {
		fields = append(fields, contract.FieldError{Field: "code", Message: "Código inválido."})
	}
	if !validDeviceName(body.Name) {
		fields = append(fields, contract.FieldError{Field: "name", Message: "Nome inválido."})
	}
	if len(fields) > 0 {
		writeError(w, StatusUnprocessable, codeInvalidParams, fields)
		return
	}
	token, err := randomHex(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	id, err := randomHex(16)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	name := strings.TrimSpace(body.Name)
	outcome, err := s.store.ExchangeEnrollment(hashSecret(body.Code), id, name, hashSecret(token), s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	if outcome.Status != statusCreated {
		writeError(w, outcome.Status, outcome.Code, nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, statusCreated, map[string]string{
		"device_id": id,
		"token":     token,
		"name":      name,
	})
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	device, ok := s.authorizeDevice(w, r)
	if !ok {
		return
	}
	raw, ok := readLimited(w, r, snapshotMax)
	if !ok {
		return
	}
	if _, err := contract.ValidateSnapshot(raw); err != nil {
		writeError(w, StatusUnprocessable, codeInvalidParams, validationFields(err))
		return
	}
	outcome, err := s.store.IngestSnapshot(device.ID, raw, s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	writeOutcome(w, outcome)
}

func (s *Server) presence(w http.ResponseWriter, r *http.Request) {
	device, ok := s.authorizeDevice(w, r)
	if !ok {
		return
	}
	raw, ok := readLimited(w, r, smallMax)
	if !ok {
		return
	}
	if _, err := contract.ValidatePresence(raw); err != nil {
		writeError(w, StatusUnprocessable, codeInvalidParams, validationFields(err))
		return
	}
	outcome, err := s.store.IngestPresence(device.ID, raw, s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return
	}
	writeOutcome(w, outcome)
}

func (s *Server) showDevice(w http.ResponseWriter, r *http.Request) {
	device, ok := s.authorizeDevice(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"id":         device.ID,
		"name":       device.Name,
		"created_at": device.CreatedAt,
	})
}

func (s *Server) allowSetup(w http.ResponseWriter, r *http.Request) bool {
	if s.setupToken != "" {
		if !fixedEqual(r.Header.Get("X-Setup-Token"), s.setupToken) {
			writeError(w, statusForbidden, codeForbidden, nil)
			return false
		}
		return true
	}
	if !s.loopback {
		writeError(w, statusForbidden, codeForbidden, nil)
		return false
	}
	return true
}

func (s *Server) operator(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		writeError(w, statusUnauthorized, codeUnauthorized, nil)
		return "", "", false
	}
	hash := hashSecret(cookie.Value)
	csrf, ok, err := s.store.LookupSession(hash, s.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return "", "", false
	}
	if !ok {
		writeError(w, statusUnauthorized, codeUnauthorized, nil)
		return "", "", false
	}
	return hash, csrf, true
}

func (s *Server) requireCSRF(w http.ResponseWriter, r *http.Request, want string) bool {
	got := r.Header.Get("X-CSRF-Token")
	if got == "" {
		if err := r.ParseForm(); err != nil {
			writeError(w, statusForbidden, codeForbidden, nil)
			return false
		}
		got = r.PostFormValue("csrf")
	}
	if !fixedEqual(got, want) {
		writeError(w, statusForbidden, codeForbidden, nil)
		return false
	}
	return true
}

func (s *Server) authorizeDevice(w http.ResponseWriter, r *http.Request) (deviceIdentity, bool) {
	raw := bearer(r)
	if raw == "" {
		writeError(w, statusUnauthorized, codeUnauthorized, nil)
		return deviceIdentity{}, false
	}
	device, found, err := s.store.DeviceByToken(hashSecret(raw))
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
		return deviceIdentity{}, false
	}
	if !found {
		writeError(w, statusUnauthorized, codeUnauthorized, nil)
		return deviceIdentity{}, false
	}
	if device.Revoked {
		writeError(w, statusUnauthorized, codeDeviceRevoked, nil)
		return deviceIdentity{}, false
	}
	return device, true
}

func (s *Server) setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.tls,
		MaxAge:   maxAge,
	})
}

func (s *Server) readPassword(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw, ok := readLimited(w, r, smallMax)
	if !ok {
		return "", false
	}
	media := r.Header.Get("Content-Type")
	trimmed := bytes.TrimSpace(raw)
	if strings.Contains(media, "json") || (len(trimmed) > 0 && trimmed[0] == '{') {
		var body struct {
			Password string `json:"password"`
		}
		if err := decodeObject(raw, &body); err != nil {
			writeError(w, StatusUnprocessable, codeInvalidParams, []contract.FieldError{{
				Field:   "body",
				Message: "json inválido",
			}})
			return "", false
		}
		return body.Password, true
	}
	values, err := url.ParseQuery(string(raw))
	if err != nil {
		writeError(w, StatusUnprocessable, codeInvalidParams, []contract.FieldError{{
			Field:   "body",
			Message: "json inválido",
		}})
		return "", false
	}
	return values.Get("password"), true
}

func passwordProblem(password string) (string, bool) {
	if utf8.RuneCountInString(password) < passwordMinRunes {
		return "A senha precisa de 12 caracteres.", true
	}
	if len(password) > passwordMaxBytes {
		return "A senha passa de 72 bytes.", true
	}
	return "", false
}

func bearer(r *http.Request) string {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

func readLimited(w http.ResponseWriter, r *http.Request, max int64) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, statusPayload, codePayloadTooLarge, nil)
			return nil, false
		}
		writeError(w, StatusUnprocessable, codeInvalidParams, []contract.FieldError{{
			Field:   "body",
			Message: "json inválido",
		}})
		return nil, false
	}
	return raw, true
}

func decodeObject(raw []byte, dest any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("json extra")
	}
	return nil
}

func validationFields(err error) []contract.FieldError {
	var validation *contract.ValidationError
	if errors.As(err, &validation) {
		return validation.Fields
	}
	return []contract.FieldError{{Field: "body", Message: "json inválido"}}
}

func writeOutcome(w http.ResponseWriter, outcome Outcome) {
	switch outcome.Status {
	case StatusOK:
		w.WriteHeader(StatusOK)
	case StatusConflict:
		code := outcome.Code
		if code == "" {
			code = CodeSnapshotConflict
		}
		writeError(w, StatusConflict, code, nil)
	case StatusUnprocessable:
		writeError(w, StatusUnprocessable, codeInvalidParams, nil)
	default:
		writeError(w, http.StatusInternalServerError, codeUnexpected, nil)
	}
}

type errorBody struct {
	Error  string      `json:"error"`
	Fields []fieldBody `json:"fields,omitempty"`
}

type fieldBody struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code string, fields []contract.FieldError) {
	body := errorBody{Error: code}
	if len(fields) > 0 {
		body.Fields = make([]fieldBody, 0, len(fields))
		for _, field := range fields {
			body.Fields = append(body.Fields, fieldBody{Field: field.Field, Message: field.Message})
		}
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"UNEXPECTED_ERROR"}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func writeHTML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

var (
	dummyOnce sync.Once
	dummyHash []byte
)

func dummyPasswordHash() []byte {
	dummyOnce.Do(func() {
		hash, err := bcrypt.GenerateFromPassword([]byte("not-the-operator"), bcryptCost)
		if err != nil {
			panic(err)
		}
		dummyHash = hash
	})
	return dummyHash
}
