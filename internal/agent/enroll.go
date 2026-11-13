package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const enrollTimeout = 10 * time.Second

var ErrAPI = errors.New("url inválida")

// Refusal is a rejected enrollment. The text never carries the code or the token.
type Refusal struct {
	Code  string
	Field string
}

func (e *Refusal) Error() string {
	return "enrolamento recusado"
}

// File is the on-disk config. It records where the token lives and does not store it.
type File struct {
	API       string `json:"api"`
	TokenFile string `json:"token_file"`
}

// Paths are the local files for one home directory.
type Paths struct {
	Config string
	Token  string
	Spool  string
}

// Enrollment is the one response that contains the device token.
type Enrollment struct {
	DeviceID string
	Token    string
	Name     string
}

// PathsFor uses the home directory from the design, not a redirected config root.
func PathsFor(home string) Paths {
	base := filepath.Join(home, ".config", "herdr-recall")
	return Paths{
		Config: filepath.Join(base, "agent.json"),
		Token:  filepath.Join(base, "device.token"),
		Spool:  filepath.Join(home, ".local", "state", "herdr-recall", "spool.json"),
	}
}

// CanonicalAPI accepts an http or https origin without userinfo or a query.
func CanonicalAPI(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", ErrAPI
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", ErrAPI
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrAPI
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

// ValidateCode checks the length before the code is sent.
func ValidateCode(code string) error {
	if len(code) < 8 || len(code) > 120 {
		return &Refusal{Code: "INVALID_PARAMS", Field: "code"}
	}
	return nil
}

// ValidateName trims and applies the same character rule as the server.
func ValidateName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if !validDeviceName(trimmed) {
		return "", &Refusal{Code: "INVALID_PARAMS", Field: "name"}
	}
	return trimmed, nil
}

func validDeviceName(name string) bool {
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

// Load reads agent.json. A missing file is os.ErrNotExist.
func Load(path string) (File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var file File
	if err := decoder.Decode(&file); err != nil {
		return File{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return File{}, errors.New("configuração inválida")
	}
	return file, nil
}

// Save writes the token and then the config. It does not open the spool.
func Save(paths Paths, api string, token string, dirSync func(string) error) error {
	if dirSync == nil {
		dirSync = syncDir
	}
	if !tokenOK(token) {
		return errors.New("token recusado")
	}
	if err := atomicWrite(paths.Token, []byte(token+"\n"), dirSync); err != nil {
		return err
	}
	body, err := marshalConfig(File{API: api, TokenFile: paths.Token})
	if err != nil {
		return err
	}
	return atomicWrite(paths.Config, body, dirSync)
}

func marshalConfig(file File) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(file); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func tokenOK(token string) bool {
	if token == "" || len(token) > 256 {
		return false
	}
	for _, char := range token {
		if char <= ' ' || char == 0x7f {
			return false
		}
	}
	return true
}

// Enroll exchanges a one-time code. A redirect is not followed: that would send the code elsewhere.
func Enroll(ctx context.Context, client *http.Client, api string, code string, name string) (Enrollment, error) {
	canonical, err := CanonicalAPI(api)
	if err != nil {
		return Enrollment{}, err
	}
	code = strings.TrimSpace(code)
	if err := ValidateCode(code); err != nil {
		return Enrollment{}, err
	}
	trimmed, err := ValidateName(name)
	if err != nil {
		return Enrollment{}, err
	}
	payload, err := json.Marshal(struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}{Code: code, Name: trimmed})
	if err != nil {
		return Enrollment{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, canonical+"/v1/devices", bytes.NewReader(payload))
	if err != nil {
		return Enrollment{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	httpClient := &http.Client{Timeout: enrollTimeout, CheckRedirect: refuseRedirect}
	if client != nil {
		copied := *client
		copied.CheckRedirect = refuseRedirect
		httpClient = &copied
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return Enrollment{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return Enrollment{}, err
	}
	if response.StatusCode != http.StatusCreated {
		return Enrollment{}, refusalFrom(response.StatusCode, raw)
	}
	var body struct {
		DeviceID string `json:"device_id"`
		Token    string `json:"token"`
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || !tokenOK(body.Token) {
		return Enrollment{}, &Refusal{Code: "INVALID_PARAMS"}
	}
	return Enrollment{DeviceID: body.DeviceID, Token: body.Token, Name: body.Name}, nil
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func refusalFrom(status int, raw []byte) error {
	var body struct {
		Error  string `json:"error"`
		Fields []struct {
			Field string `json:"field"`
		} `json:"fields"`
	}
	_ = json.Unmarshal(raw, &body)
	refusal := &Refusal{Code: body.Error}
	if len(body.Fields) > 0 {
		refusal.Field = body.Fields[0].Field
	}
	if refusal.Code == "" {
		refusal.Code = "UNEXPECTED_ERROR"
	}
	if status == http.StatusUnprocessableEntity && refusal.Code == "UNEXPECTED_ERROR" {
		refusal.Code = "INVALID_PARAMS"
	}
	return refusal
}
