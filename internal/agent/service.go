package agent

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// UnitName is the systemd user unit.
	UnitName = "herdr-recall.service"
	// LaunchLabel is the launchd label. The word agent is launchd's, not a binary name.
	LaunchLabel = "dev.herdr-recall.agent"
)

var (
	ErrUnsupported     = errors.New("plataforma sem serviço")
	ErrTemporaryBinary = errors.New("binário temporário")
	ErrAgentName       = errors.New("executável agent")
	ErrBinaryPath      = errors.New("caminho do binário")
)

// CommandRunner runs one command without a shell.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// ExecRunner is the process runner used outside tests.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Manager installs the local capture service for one user.
type Manager struct {
	GOOS     string
	Home     string
	UID      int
	User     string
	Exe      string
	Runner   CommandRunner
	LookPath func(string) (string, error)
}

// Report is the unit text and the commands install would run.
type Report struct {
	File     string
	Content  string
	Commands []string
	Notes    []string
	Changed  bool
}

// InstallOptions selects a dry run.
type InstallOptions struct {
	DryRun bool
}

// NewManager builds the manager for this process.
func NewManager(goos string, home string, exe string, user string, uid int) Manager {
	return Manager{
		GOOS:     goos,
		Home:     home,
		UID:      uid,
		User:     user,
		Exe:      exe,
		Runner:   ExecRunner{},
		LookPath: exec.LookPath,
	}
}

func (m Manager) kind() (string, error) {
	switch m.GOOS {
	case "linux":
		return "systemd", nil
	case "darwin":
		return "launchd", nil
	default:
		return "", ErrUnsupported
	}
}

// FilePath is the unit or plist path.
func (m Manager) FilePath() (string, error) {
	kind, err := m.kind()
	if err != nil {
		return "", err
	}
	if kind == "systemd" {
		return filepath.Join(m.Home, ".config", "systemd", "user", UnitName), nil
	}
	return filepath.Join(m.Home, "Library", "LaunchAgents", LaunchLabel+".plist"), nil
}

func (m Manager) logPath() string {
	return filepath.Join(m.Home, "Library", "Logs", "herdr-recall.log")
}

func (m Manager) domain() string {
	return fmt.Sprintf("gui/%d", m.UID)
}

func (m Manager) userName() string {
	if m.User != "" {
		return m.User
	}
	return "$USER"
}

// ServicePATH is the PATH written into the unit. A user service starts with a short PATH,
// so the directory of herdr from the installing shell is included.
func (m Manager) ServicePATH() string {
	var dirs []string
	seen := map[string]bool{}
	add := func(dir string) {
		if dir == "" || !filepath.IsAbs(dir) || seen[dir] {
			return
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}
	add(filepath.Dir(m.Exe))
	if m.LookPath != nil {
		if found, err := m.LookPath("herdr"); err == nil {
			add(filepath.Dir(found))
		}
	}
	add(filepath.Join(m.Home, ".local", "bin"))
	if m.GOOS == "darwin" {
		add("/opt/homebrew/bin")
	}
	for _, dir := range []string{"/usr/local/bin", "/usr/bin", "/bin"} {
		add(dir)
	}
	return strings.Join(dirs, ":")
}

func (m Manager) ready() error {
	if _, err := m.kind(); err != nil {
		return err
	}
	if !filepath.IsAbs(m.Exe) {
		return ErrBinaryPath
	}
	if strings.ContainsAny(m.Exe, "\r\n") || strings.ContainsAny(m.Home, "\r\n") {
		return ErrBinaryPath
	}
	if strings.Contains(m.Exe, "go-build") {
		return ErrTemporaryBinary
	}
	if filepath.Base(m.Exe) == "agent" {
		return ErrAgentName
	}
	return nil
}

// Render returns the unit text. It does not write it.
func (m Manager) Render() (string, error) {
	if err := m.ready(); err != nil {
		return "", err
	}
	path := m.ServicePATH()
	kind, err := m.kind()
	if err != nil {
		return "", err
	}
	if kind == "systemd" {
		return renderUnit(m.Exe, path), nil
	}
	return renderPlist(m.Exe, path, m.logPath()), nil
}

func renderUnit(exe string, path string) string {
	return strings.Join([]string{
		"[Unit]",
		"Description=Captura local do Herdr",
		"",
		"[Service]",
		"ExecStart=" + unitQuote(exe, true) + " run",
		"Environment=" + unitQuote("PATH="+path, false),
		"Restart=on-failure",
		"RestartSec=60",
		"RestartPreventExitStatus=3",
		"",
		"[Install]",
		"WantedBy=default.target",
		"",
	}, "\n")
}

func unitQuote(value string, exec bool) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "%", "%%")
	if exec {
		value = strings.ReplaceAll(value, "$", "$$")
	}
	return `"` + value + `"`
}

func renderPlist(exe string, path string, logPath string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + LaunchLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + xmlText(exe) + `</string>
    <string>run</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>` + xmlText(path) + `</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>ThrottleInterval</key>
  <integer>60</integer>
  <key>StandardOutPath</key>
  <string>` + xmlText(logPath) + `</string>
  <key>StandardErrorPath</key>
  <string>` + xmlText(logPath) + `</string>
</dict>
</plist>
`
}

func xmlText(value string) string {
	var buffer bytes.Buffer
	_ = xml.EscapeText(&buffer, []byte(value))
	return buffer.String()
}

var (
	plistExe  = regexp.MustCompile(`(?s)<key>ProgramArguments</key>\s*<array>\s*<string>(.*?)</string>`)
	plistPath = regexp.MustCompile(`(?s)<key>PATH</key>\s*<string>(.*?)</string>`)
)

// InstalledExe reads the binary from a unit or a plist.
func InstalledExe(content string) string {
	if match := plistExe.FindStringSubmatch(content); match != nil {
		return html.UnescapeString(match[1])
	}
	for _, line := range strings.Split(content, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart=")
		if ok {
			return firstToken(rest)
		}
	}
	return ""
}

// InstalledPATH reads the PATH from a unit or a plist.
func InstalledPATH(content string) string {
	if match := plistPath.FindStringSubmatch(content); match != nil {
		return html.UnescapeString(match[1])
	}
	for _, line := range strings.Split(content, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Environment=")
		if ok {
			return strings.TrimPrefix(firstToken(rest), "PATH=")
		}
	}
	return ""
}

func firstToken(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, `"`) {
		token, _, _ := strings.Cut(text, " ")
		return token
	}
	var out strings.Builder
	for i := 1; i < len(text); i++ {
		char := text[i]
		if char == '\\' && i+1 < len(text) {
			out.WriteByte(text[i+1])
			i++
			continue
		}
		if char == '"' {
			break
		}
		out.WriteByte(char)
	}
	return strings.ReplaceAll(strings.ReplaceAll(out.String(), "%%", "%"), "$$", "$")
}

// Install writes the unit and starts it. Dry-run only fills the report.
func (m Manager) Install(ctx context.Context, opts InstallOptions) (Report, error) {
	kind, err := m.kind()
	if err != nil {
		return Report{}, err
	}
	content, err := m.Render()
	if err != nil {
		return Report{}, err
	}
	file, err := m.FilePath()
	if err != nil {
		return Report{}, err
	}
	report := Report{File: file, Content: content}
	_, statErr := os.Stat(file)
	existed := statErr == nil
	var steps [][]string
	if kind == "systemd" {
		steps = [][]string{
			{"systemctl", "--user", "daemon-reload"},
			{"systemctl", "--user", "enable", UnitName},
			{"systemctl", "--user", "restart", UnitName},
		}
	} else {
		if existed {
			steps = append(steps, []string{"launchctl", "bootout", m.domain() + "/" + LaunchLabel})
		}
		steps = append(steps,
			[]string{"launchctl", "bootstrap", m.domain(), file},
			[]string{"launchctl", "kickstart", "-k", m.domain() + "/" + LaunchLabel},
		)
	}
	for _, step := range steps {
		report.Commands = append(report.Commands, strings.Join(step, " "))
	}
	if opts.DryRun {
		return report, nil
	}
	if m.Runner == nil {
		return report, errors.New("sem executor")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return report, err
	}
	if kind == "launchd" {
		if err := os.MkdirAll(filepath.Dir(m.logPath()), 0o755); err != nil {
			return report, err
		}
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		return report, err
	}
	report.Changed = true
	for i, step := range steps {
		out, runErr := m.Runner.Run(ctx, step[0], step[1:]...)
		if runErr != nil {
			if len(step) > 1 && step[1] == "bootout" {
				continue
			}
			return report, commandError(report.Commands[i], out, runErr)
		}
	}
	report.Notes = m.afterInstall(ctx, kind)
	return report, nil
}

func commandError(line string, out string, err error) error {
	message := line + " falhou"
	if out != "" {
		message += ": " + out
	}
	if err != nil && out == "" {
		message += ": " + err.Error()
	}
	return errors.New(message)
}

func (m Manager) afterInstall(ctx context.Context, kind string) []string {
	if kind != "systemd" {
		return []string{"Log: tail -f " + m.logPath()}
	}
	notes := []string{"Log: journalctl --user -u herdr-recall -f"}
	if m.lingerOff(ctx) {
		notes = append(notes, "O linger está desligado, então o serviço para ao sair da sessão. Para continuar: loginctl enable-linger "+m.userName())
	}
	return notes
}

func (m Manager) lingerOff(ctx context.Context) bool {
	value, known := m.lingerValue(ctx)
	return known && value == "no"
}

func (m Manager) lingerValue(ctx context.Context) (string, bool) {
	if m.Runner == nil {
		return "", false
	}
	out, err := m.Runner.Run(ctx, "loginctl", "show-user", fmt.Sprint(m.UID), "-p", "Linger", "--value")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(out), true
}

// Uninstall removes the unit. Captures, config, and the token stay.
func (m Manager) Uninstall(ctx context.Context) (Report, error) {
	kind, err := m.kind()
	if err != nil {
		return Report{}, err
	}
	file, err := m.FilePath()
	if err != nil {
		return Report{}, err
	}
	report := Report{File: file}
	_, statErr := os.Stat(file)
	present := statErr == nil
	if m.Runner == nil {
		return report, errors.New("sem executor")
	}
	if kind == "systemd" {
		if present {
			_, _ = m.Runner.Run(ctx, "systemctl", "--user", "disable", "--now", UnitName)
		}
	} else {
		_, _ = m.Runner.Run(ctx, "launchctl", "bootout", m.domain()+"/"+LaunchLabel)
	}
	if present {
		if err := os.Remove(file); err != nil {
			return report, err
		}
		report.Changed = true
	}
	if kind == "systemd" && present {
		_, _ = m.Runner.Run(ctx, "systemctl", "--user", "daemon-reload")
		_, _ = m.Runner.Run(ctx, "systemctl", "--user", "reset-failed", UnitName)
	}
	if report.Changed {
		report.Notes = []string{"A configuração e as capturas ficaram."}
	}
	return report, nil
}

// Status is one doctor row.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Check is one read-only doctor finding.
type Check struct {
	Name   string
	Status Status
	Detail string
	Fix    string
}

// Failed reports whether any check is a failure. A warning is not a failure.
func Failed(checks []Check) bool {
	for _, check := range checks {
		if check.Status == StatusFail {
			return true
		}
	}
	return false
}

var launchState = regexp.MustCompile(`state = (\w+)`)

// Doctor reads the unit, the PATH, and dirsync-warn. It does not call the server
// and it does not enable linger.
func (m Manager) Doctor(ctx context.Context) []Check {
	var checks []Check
	add := func(name string, status Status, detail string, fix string) {
		checks = append(checks, Check{Name: name, Status: status, Detail: detail, Fix: fix})
	}
	kind, err := m.kind()
	if err != nil {
		add("plataforma", StatusFail, "sem serviço neste corte", "")
		return checks
	}
	tool := "systemctl"
	label := "systemd de usuário"
	if kind == "launchd" {
		tool = "launchctl"
		label = "launchd"
	}
	if m.LookPath != nil {
		if _, lookErr := m.LookPath(tool); lookErr != nil {
			add("plataforma", StatusFail, tool+" ausente", "")
			return checks
		}
	}
	add("plataforma", StatusOK, label, "")

	file, fileErr := m.FilePath()
	if fileErr != nil {
		add("unidade", StatusFail, "caminho da unidade", "")
		return checks
	}
	raw, readErr := os.ReadFile(file)
	installed := readErr == nil
	if !installed {
		add("unidade", StatusFail, "não instalada", "herdr-recall service install")
	} else {
		add("unidade", StatusOK, "instalada", "")
		pointed := InstalledExe(string(raw))
		switch {
		case pointed == "":
			add("binário", StatusFail, "a unidade não tem comando", "herdr-recall service install")
		case pointed != m.Exe:
			add("binário", StatusWarn, "a unidade aponta para outro binário", "herdr-recall service install")
		default:
			info, statErr := os.Stat(pointed)
			if statErr != nil || info.IsDir() {
				add("binário", StatusFail, "o binário da unidade não existe", "herdr-recall service install")
			} else {
				add("binário", StatusOK, "o binário da unidade existe", "")
			}
		}
		checks = append(checks, m.stateChecks(ctx, kind)...)
		if found, ok := herdrOnPath(InstalledPATH(string(raw))); ok {
			add("herdr", StatusOK, found, "")
		} else {
			add("herdr", StatusFail, "ausente no PATH da unidade", "herdr-recall service install")
		}
	}
	if kind == "systemd" {
		value, known := m.lingerValue(ctx)
		switch {
		case !known:
			add("linger", StatusWarn, "não foi possível ler o linger", "")
		case value == "no":
			add("linger", StatusWarn, "o serviço para ao sair da sessão", "loginctl enable-linger "+m.userName())
		default:
			add("linger", StatusOK, "o serviço pode seguir depois do logout", "")
		}
	}
	warnPath := filepath.Join(m.Home, ".local", "state", "herdr-recall", "dirsync-warn")
	if info, statErr := os.Stat(warnPath); statErr == nil && !info.IsDir() {
		add("dirsync", StatusWarn, "o sync de diretório foi recusado", "")
	}
	return checks
}

func (m Manager) stateChecks(ctx context.Context, kind string) []Check {
	if m.Runner == nil {
		return []Check{{Name: "serviço", Status: StatusFail, Detail: "sem executor"}}
	}
	if kind == "systemd" {
		active, _ := m.Runner.Run(ctx, "systemctl", "--user", "is-active", UnitName)
		enabled, _ := m.Runner.Run(ctx, "systemctl", "--user", "is-enabled", UnitName)
		var checks []Check
		if strings.TrimSpace(active) == "active" {
			checks = append(checks, Check{Name: "serviço", Status: StatusOK, Detail: "ativo"})
		} else {
			state := strings.TrimSpace(active)
			if state == "" {
				state = "desconhecido"
			}
			checks = append(checks, Check{Name: "serviço", Status: StatusFail, Detail: state, Fix: "journalctl --user -u herdr-recall -n 30"})
		}
		if strings.TrimSpace(enabled) == "enabled" {
			checks = append(checks, Check{Name: "início", Status: StatusOK, Detail: "ligado no login"})
		} else {
			checks = append(checks, Check{Name: "início", Status: StatusWarn, Detail: "não liga no login", Fix: "herdr-recall service install"})
		}
		return checks
	}
	out, err := m.Runner.Run(ctx, "launchctl", "print", m.domain()+"/"+LaunchLabel)
	if err != nil {
		return []Check{{Name: "serviço", Status: StatusFail, Detail: "não carregado", Fix: "herdr-recall service install"}}
	}
	state := "desconhecido"
	if match := launchState.FindStringSubmatch(out); match != nil {
		state = match[1]
	}
	if state == "running" {
		return []Check{{Name: "serviço", Status: StatusOK, Detail: "em execução"}}
	}
	return []Check{{Name: "serviço", Status: StatusFail, Detail: state, Fix: "herdr-recall service install"}}
}

func herdrOnPath(path string) (string, bool) {
	for _, dir := range strings.Split(path, ":") {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, "herdr")
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		return candidate, true
	}
	return "", false
}
