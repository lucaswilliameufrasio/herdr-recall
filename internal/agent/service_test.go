package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDryRunPrintsTheUnitAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	herdr := filepath.Join(home, "bin", "herdr")
	if err := os.MkdirAll(filepath.Dir(herdr), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(herdr, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(home, "herdr-recall")
	runner := &fakeCmd{fail: true}
	manager := testManager(home, exe, herdr, runner)
	serviceTestEnrollment(t, home, "http://127.0.0.1:12345")
	report, err := manager.Install(context.Background(), InstallOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(report.File); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("o dry-run gravou a unidade")
	}
	if !strings.Contains(report.Content, "RestartPreventExitStatus=3") || !strings.Contains(report.Content, "Restart=on-failure") {
		t.Fatalf("unidade: %s", report.Content)
	}
	if !strings.Contains(report.Content, filepath.Dir(herdr)) {
		t.Fatal("o PATH não tem o diretório do herdr")
	}
	if strings.Contains(report.Content, "agent") {
		t.Fatalf("a unidade cita agent: %s", report.Content)
	}
	if len(report.Commands) != 3 {
		t.Fatalf("comandos: %v", report.Commands)
	}
}

func TestInstallRefusesATemporaryOrAgentBinary(t *testing.T) {
	home := t.TempDir()
	manager := testManager(home, filepath.Join(home, "go-build123", "herdr-recall"), "", &fakeCmd{})
	if _, err := manager.Install(context.Background(), InstallOptions{}); !errors.Is(err, ErrTemporaryBinary) {
		t.Fatal(err)
	}
	manager.Exe = filepath.Join(home, "agent")
	if _, err := manager.Install(context.Background(), InstallOptions{}); !errors.Is(err, ErrAgentName) {
		t.Fatal(err)
	}
	manager.GOOS = "windows"
	manager.Exe = filepath.Join(home, "herdr-recall")
	if _, err := manager.Install(context.Background(), InstallOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", UnitName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a recusa gravou a unidade")
	}
}

func TestInstallWritesAndUninstallKeepsCaptures(t *testing.T) {
	home := t.TempDir()
	herdr := filepath.Join(home, "bin", "herdr")
	if err := os.MkdirAll(filepath.Dir(herdr), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(herdr, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(home, "my bin", "herdr-recall")
	runner := &fakeCmd{linger: "no"}
	manager := testManager(home, exe, herdr, runner)
	serviceTestEnrollment(t, home, "http://127.0.0.1:12345")
	report, err := manager.Install(context.Background(), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(report.File)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("modo: %v %v", info, err)
	}
	if !strings.Contains(report.Content, `"`+exe+`" run`) {
		t.Fatalf("exec: %s", report.Content)
	}
	joined := strings.Join(runner.calls, "\n")
	for _, part := range []string{"daemon-reload", "enable " + UnitName, "restart " + UnitName} {
		if !strings.Contains(joined, part) {
			t.Fatalf("chamadas: %s", joined)
		}
	}
	if !strings.Contains(strings.Join(report.Notes, "\n"), "enable-linger ana") {
		t.Fatalf("notas: %v", report.Notes)
	}
	latest := filepath.Join(home, ".local", "state", "herdr-recall", "latest.json")
	if err := os.MkdirAll(filepath.Dir(latest), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(latest, []byte(`{"panes":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := PathsFor(home)
	if err := os.WriteFile(paths.Spool, []byte("fixture-spool"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := manager.Uninstall(context.Background())
	if err != nil || !removed.Changed {
		t.Fatal(err)
	}
	if _, err := os.Stat(report.File); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a unidade ficou")
	}
	if _, err := os.Stat(latest); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Config, paths.Token, paths.Spool} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("uninstall removeu arquivos de dados")
		}
	}
}

func TestDoctorWarnsAboutDirsyncWithoutFailing(t *testing.T) {
	home := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		_, _ = w.Write([]byte(`{"id":"fixture","name":"Máquina","created_at":"2026-11-13T00:00:00Z"}`))
	}))
	defer server.Close()
	serviceTestEnrollment(t, home, server.URL)
	herdr := filepath.Join(home, "bin", "herdr")
	if err := os.MkdirAll(filepath.Dir(herdr), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(herdr, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(home, "herdr-recall")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager := testManager(home, exe, herdr, &fakeCmd{linger: "yes"})
	if _, err := manager.Install(context.Background(), InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	warn := filepath.Join(home, ".local", "state", "herdr-recall", "dirsync-warn")
	if err := os.MkdirAll(filepath.Dir(warn), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(warn, []byte("aviso\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checks := manager.Doctor(context.Background())
	if Failed(checks) {
		t.Fatalf("aviso virou falha: %+v", checks)
	}
	if !hasCheck(checks, "dirsync", StatusWarn) || !hasCheck(checks, "herdr", StatusOK) {
		t.Fatalf("checks: %+v", checks)
	}
}

func TestDoctorFailsWhenHerdrIsMissing(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "herdr-recall")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(home, ".config", "systemd", "user", UnitName)
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(renderUnit(exe, filepath.Join(home, "empty"))), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := testManager(home, exe, "", &fakeCmd{linger: "yes"})
	checks := manager.Doctor(context.Background())
	if !Failed(checks) || !hasCheck(checks, "herdr", StatusFail) {
		t.Fatalf("checks: %+v", checks)
	}
}

func TestDarwinDryRunUsesLaunchdVocabulary(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "herdr-recall")
	manager := testManager(home, exe, "", &fakeCmd{fail: true})
	manager.GOOS = "darwin"
	serviceTestEnrollment(t, home, "http://127.0.0.1:12345")
	report, err := manager.Install(context.Background(), InstallOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.Content, LaunchLabel) || !strings.Contains(report.Content, "<string>run</string>") || !strings.Contains(report.Content, "<integer>60</integer>") {
		t.Fatalf("plist: %s", report.Content)
	}
	if strings.Contains(report.Content, "<string>agent</string>") {
		t.Fatal("o plist executa agent")
	}
	if _, statErr := os.Stat(report.File); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("o dry-run gravou o plist")
	}
}

func serviceTestEnrollment(t *testing.T, home, api string) {
	t.Helper()
	if err := Save(PathsFor(home), api, "test-token", nil); err != nil {
		t.Fatal(err)
	}
}

func testManager(home string, exe string, herdr string, runner *fakeCmd) Manager {
	return Manager{
		GOOS:   "linux",
		Home:   home,
		UID:    1000,
		User:   "ana",
		Exe:    exe,
		Runner: runner,
		LookPath: func(name string) (string, error) {
			switch name {
			case "herdr":
				if herdr == "" {
					return "", errors.New("ausente")
				}
				return herdr, nil
			case "systemctl", "launchctl":
				return "/usr/bin/" + name, nil
			default:
				return "", errors.New("ausente")
			}
		},
	}
}

func hasCheck(checks []Check, name string, status Status) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}

type fakeCmd struct {
	calls   []string
	linger  string
	active  string
	enabled string
	fail    bool
}

func (f *fakeCmd) Run(_ context.Context, name string, args ...string) (string, error) {
	return f.run(name, args...)
}

func (f *fakeCmd) run(name string, args ...string) (string, error) {
	if f.fail {
		return "", errors.New("não devia executar")
	}
	line := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, line)
	switch {
	case strings.Contains(line, "is-active"):
		if f.active != "" {
			return f.active, nil
		}
		return "active", nil
	case strings.Contains(line, "is-enabled"):
		if f.enabled != "" {
			return f.enabled, nil
		}
		return "enabled", nil
	case strings.Contains(line, "Linger"):
		if f.linger != "" {
			return f.linger, nil
		}
		return "yes", nil
	case strings.Contains(line, "launchctl print"):
		return "state = running", nil
	default:
		return "", nil
	}
}
