package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

const (
	sessionCookie      = "recall_session"
	sessionLife        = 12 * time.Hour
	sessionMaxAge      = 12 * 60 * 60
	enrollmentLife     = 10 * time.Minute
	enrollmentWindow   = time.Hour
	enrollmentCap      = 5
	enrollmentLength   = 10
	bcryptCost         = 12
	passwordMinRunes   = 12
	passwordMaxBytes   = 72
	statusCreated      = 201
	statusUnauthorized = 401
	statusForbidden    = 403
	statusNotFound     = 404
	statusTooMany      = 429
	statusPayload      = 413

	codeUnauthorized      = "UNAUTHORIZED"
	codeForbidden         = "FORBIDDEN"
	codeNotFound          = "NOT_FOUND"
	codeInvalidParams     = "INVALID_PARAMS"
	codeRateLimited       = "RATE_LIMITED"
	codeUnexpected        = "UNEXPECTED_ERROR"
	codePayloadTooLarge   = "PAYLOAD_TOO_LARGE"
	codeEnrollmentInvalid = "ENROLLMENT_INVALID"
	codeEnrollmentUsed    = "ENROLLMENT_USED"
	codeEnrollmentExpired = "ENROLLMENT_EXPIRED"
	codeDeviceRevoked     = "DEVICE_REVOKED"
	crockfordAlphabet     = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

type deviceIdentity struct {
	ID        string
	Name      string
	CreatedAt string
	Revoked   bool
}

func hashSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func fixedEqual(got string, want string) bool {
	if len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func validDeviceName(name string) bool {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 64 {
		return false
	}
	for _, char := range name {
		if unicode.IsLetter(char) || unicode.IsNumber(char) || char == ' ' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func randomHex(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func enrollmentCode() (string, error) {
	raw := make([]byte, enrollmentLength)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i, value := range raw {
		raw[i] = crockfordAlphabet[value&31]
	}
	return string(raw), nil
}

func (s *Store) CreateOperator(passwordHash string, now time.Time) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	var id int
	err = tx.QueryRow(`SELECT id FROM operator WHERE id = 1`).Scan(&id)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if _, err := tx.Exec(
		`INSERT INTO operator (id, password_hash, created_at) VALUES (1, ?, ?)`,
		passwordHash, stamp(now),
	); err != nil {
		return false, fmt.Errorf("criar operador: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) OperatorHash() (string, bool, error) {
	var hash string
	err := s.db.QueryRow(`SELECT password_hash FROM operator WHERE id = 1`).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return hash, true, nil
}

func (s *Store) OpenSession(tokenHash string, csrf string, now time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (token_hash, csrf, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash, csrf, stamp(now), stamp(now.Add(sessionLife)),
	)
	if err != nil {
		return fmt.Errorf("abrir sessão: %w", err)
	}
	return nil
}

func (s *Store) LookupSession(tokenHash string, now time.Time) (string, bool, error) {
	var csrf string
	var expires string
	err := s.db.QueryRow(`SELECT csrf, expires_at FROM sessions WHERE token_hash = ?`, tokenHash).Scan(&csrf, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if expires < stamp(now) {
		return "", false, nil
	}
	return csrf, true, nil
}

func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	if err != nil {
		return fmt.Errorf("apagar sessão: %w", err)
	}
	return nil
}

func (s *Store) CreateEnrollmentCode(codeHash string, now time.Time) (string, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	var n int
	since := stamp(now.Add(-enrollmentWindow))
	if err := tx.QueryRow(`SELECT COUNT(*) FROM enrollment_codes WHERE created_at >= ?`, since).Scan(&n); err != nil {
		return "", false, err
	}
	if n >= enrollmentCap {
		return "", true, nil
	}
	expires := stamp(now.Add(enrollmentLife))
	if _, err := tx.Exec(
		`INSERT INTO enrollment_codes (code_hash, created_at, expires_at) VALUES (?, ?, ?)`,
		codeHash, stamp(now), expires,
	); err != nil {
		return "", false, fmt.Errorf("criar código: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return expires, false, nil
}

func (s *Store) ExchangeEnrollment(codeHash string, deviceID string, name string, tokenHash string, now time.Time) (Outcome, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Outcome{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	var expires string
	var used sql.NullString
	err = tx.QueryRow(`SELECT expires_at, used_at FROM enrollment_codes WHERE code_hash = ?`, codeHash).Scan(&expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{Status: statusUnauthorized, Code: codeEnrollmentInvalid}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	if used.Valid {
		return Outcome{Status: statusUnauthorized, Code: codeEnrollmentUsed}, nil
	}
	if expires < stamp(now) {
		return Outcome{Status: statusUnauthorized, Code: codeEnrollmentExpired}, nil
	}
	result, err := tx.Exec(
		`UPDATE enrollment_codes SET used_at = ? WHERE code_hash = ? AND used_at IS NULL`,
		stamp(now), codeHash,
	)
	if err != nil {
		return Outcome{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return Outcome{}, err
	}
	if n != 1 {
		return Outcome{Status: statusUnauthorized, Code: codeEnrollmentUsed}, nil
	}
	if _, err := tx.Exec(
		`INSERT INTO devices (id, name, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		deviceID, name, tokenHash, stamp(now),
	); err != nil {
		return Outcome{}, fmt.Errorf("criar máquina: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Outcome{}, err
	}
	return Outcome{Status: statusCreated}, nil
}

func (s *Store) DeviceByToken(tokenHash string) (deviceIdentity, bool, error) {
	var row deviceIdentity
	var revoked sql.NullString
	err := s.db.QueryRow(
		`SELECT id, name, created_at, revoked_at FROM devices WHERE token_hash = ?`,
		tokenHash,
	).Scan(&row.ID, &row.Name, &row.CreatedAt, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return deviceIdentity{}, false, nil
	}
	if err != nil {
		return deviceIdentity{}, false, err
	}
	row.Revoked = revoked.Valid
	return row, true, nil
}

func (s *Store) DeviceByID(id string) (deviceIdentity, bool, error) {
	var row deviceIdentity
	var revoked sql.NullString
	err := s.db.QueryRow(
		`SELECT id, name, created_at, revoked_at FROM devices WHERE id = ?`,
		id,
	).Scan(&row.ID, &row.Name, &row.CreatedAt, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return deviceIdentity{}, false, nil
	}
	if err != nil {
		return deviceIdentity{}, false, err
	}
	row.Revoked = revoked.Valid
	return row, true, nil
}

func (s *Store) RevokeDevice(id string, now time.Time) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	var revoked sql.NullString
	err = tx.QueryRow(`SELECT revoked_at FROM devices WHERE id = ?`, id).Scan(&revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if revoked.Valid {
		return true, nil
	}
	if _, err := tx.Exec(
		`UPDATE devices SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		stamp(now), id,
	); err != nil {
		return false, fmt.Errorf("revogar máquina: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
