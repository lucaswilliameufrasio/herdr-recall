package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const doctorTimeout = 4 * time.Second
const doctorBodyLimit = 64 << 10

// serviceCredentials validates enrolled local configuration without returning
// filesystem errors, which could contain sensitive data.
func (m Manager) serviceCredentials() (string, string, error) {
	config, err := Load(PathsFor(m.Home).Config)
	if err != nil {
		return "", "", ErrServiceConfig
	}
	api, err := CanonicalAPI(config.API)
	if err != nil || config.TokenFile == "" || !filepath.IsAbs(config.TokenFile) {
		return "", "", ErrServiceConfig
	}
	parsed, err := url.Parse(api)
	if err != nil {
		return "", "", ErrServiceConfig
	}
	if parsed.Scheme == "http" {
		host := parsed.Hostname()
		ip := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			return "", "", ErrServiceConfig
		}
	}
	f, err := os.Open(config.TokenFile)
	if err != nil {
		return "", "", ErrServiceToken
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", "", ErrServiceToken
	}
	raw, err := io.ReadAll(io.LimitReader(f, 258))
	if err != nil || len(raw) > 257 {
		return "", "", ErrServiceToken
	}
	token := strings.TrimSuffix(string(raw), "\n")
	token = strings.TrimSuffix(token, "\r")
	if !tokenOK(token) {
		return "", "", ErrServiceToken
	}
	return api, token, nil
}

// queueChecks reads atomic snapshots of the queue and quarantine without
// modifying either file or revealing any payload.
func (m Manager) queueChecks() []Check {
	stateDir := filepath.Join(m.Home, ".local", "state", "herdr-recall")
	queue, err := loadSpool(filepath.Join(stateDir, "spool.json"))
	if err != nil {
		return []Check{{Name: "fila", Status: StatusFail, Detail: "não foi possível ler a fila"}}
	}
	checks := []Check{{Name: "fila", Status: StatusOK, Detail: fmt.Sprintf("%d itens pendentes", len(queue.Items))}}
	conflicts, err := loadSpool(filepath.Join(stateDir, "spool.conflict"))
	if err != nil {
		return append(checks, Check{Name: "conflitos", Status: StatusFail, Detail: "não foi possível ler a quarentena"})
	}
	status := StatusOK
	if len(conflicts.Items) > 0 {
		status = StatusWarn
	}
	return append(checks, Check{Name: "conflitos", Status: status, Detail: fmt.Sprintf("%d itens em quarentena", len(conflicts.Items))})
}

func (m Manager) serverChecks(ctx context.Context) []Check {
	api, token, err := m.serviceCredentials()
	if err != nil {
		if errors.Is(err, ErrServiceToken) {
			return []Check{
				{Name: "configuração", Status: StatusOK, Detail: "configuração encontrada"},
				{Name: "token", Status: StatusFail, Detail: "token ausente, inválido ou inseguro", Fix: "herdr-recall setup"},
			}
		}
		return []Check{{Name: "configuração", Status: StatusFail, Detail: "configuração ausente ou inválida", Fix: "herdr-recall setup"}}
	}
	checks := []Check{{Name: "configuração", Status: StatusOK, Detail: "configuração encontrada"}}
	client := &http.Client{Timeout: doctorTimeout, CheckRedirect: refuseRedirect}
	status, body, err := doctorGet(ctx, client, api+"/healthz", "")
	if err != nil || status != http.StatusOK || strings.TrimSpace(string(body)) != "ok" {
		return append(checks, Check{Name: "servidor", Status: StatusFail, Detail: "servidor não respondeu ao healthz"})
	}
	checks = append(checks, Check{Name: "servidor", Status: StatusOK, Detail: "healthz respondeu"})
	status, body, err = doctorGet(ctx, client, api+"/v1/device", token)
	if err != nil {
		return append(checks, Check{Name: "token", Status: StatusFail, Detail: "não foi possível verificar o dispositivo"})
	}
	switch status {
	case http.StatusUnauthorized:
		return append(checks, Check{Name: "token", Status: StatusFail, Detail: "token recusado ou dispositivo revogado", Fix: "herdr-recall setup"})
	case http.StatusNotFound:
		return append(checks, Check{Name: "token", Status: StatusWarn, Detail: "servidor não oferece verificação de dispositivo"})
	case http.StatusOK:
		var device struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			CreatedAt string `json:"created_at"`
		}
		if json.Unmarshal(body, &device) != nil || device.ID == "" || device.Name == "" || device.CreatedAt == "" {
			return append(checks, Check{Name: "token", Status: StatusFail, Detail: "resposta de dispositivo inválida"})
		}
		return append(checks, Check{Name: "token", Status: StatusOK, Detail: "dispositivo autorizado"})
	default:
		return append(checks, Check{Name: "token", Status: StatusFail, Detail: "não foi possível verificar o dispositivo"})
	}
}

func doctorGet(ctx context.Context, client *http.Client, endpoint, token string) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, doctorBodyLimit+1))
	if err != nil || len(body) > doctorBodyLimit {
		return 0, nil, errors.New("resposta inválida")
	}
	return response.StatusCode, body, nil
}
