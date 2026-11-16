package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeBinary answers --version like the real agent; v is "" for a pre-0.4.0
// binary that only prints its name.
func fakeBinary(name, v string) []byte {
	line := name
	if v != "" {
		line = name + " " + v
	}
	return []byte("#!/bin/sh\necho '" + line + "'\n")
}

func writeBundle(t *testing.T, dir, v string) {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	script, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), script, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"herdr-recall", "herdr-recall-server"} {
		if err := os.WriteFile(filepath.Join(dir, name), fakeBinary(name, v), 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func installed(t *testing.T, dest string) string {
	t.Helper()
	out, err := exec.Command(filepath.Join(dest, "herdr-recall")).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func runInstaller(t *testing.T, bundle, dest string, flags ...string) (string, error) {
	t.Helper()
	args := append([]string{filepath.Join(bundle, "install.sh"), "--bin-dir", dest}, flags...)
	cmd := exec.Command("sh", args...)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestInstallerDecidesByVersion(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "bin")
	for _, v := range []string{"v0.3.9", "v0.4.0", "v0.10.0"} {
		writeBundle(t, filepath.Join(root, v), v)
	}

	out, err := runInstaller(t, filepath.Join(root, "v0.4.0"), dest)
	if err != nil || !strings.Contains(out, "herdr-recall v0.4.0 instalado") {
		t.Fatalf("instalação: %v %s", err, out)
	}
	for _, name := range []string{"herdr-recall", "herdr-recall-server"} {
		info, err := os.Stat(filepath.Join(dest, name))
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatal("binário ausente ou sem permissão de execução")
		}
	}

	out, err = runInstaller(t, filepath.Join(root, "v0.4.0"), dest)
	if err != nil || !strings.Contains(out, "já está instalado") {
		t.Fatalf("mesma versão: %v %s", err, out)
	}

	out, err = runInstaller(t, filepath.Join(root, "v0.3.9"), dest)
	if err == nil || !strings.Contains(out, "--allow-downgrade") || installed(t, dest) != "herdr-recall v0.4.0" {
		t.Fatalf("downgrade aceito: %v %s", err, out)
	}

	// 0.10.0 > 0.4.0 numerically, not lexically.
	out, err = runInstaller(t, filepath.Join(root, "v0.10.0"), dest)
	if err != nil || !strings.Contains(out, "atualizar de v0.4.0 para v0.10.0") || installed(t, dest) != "herdr-recall v0.10.0" {
		t.Fatalf("atualização: %v %s", err, out)
	}

	out, err = runInstaller(t, filepath.Join(root, "v0.3.9"), dest, "--allow-downgrade")
	if err != nil || !strings.Contains(out, "Aviso: voltando de v0.10.0 para v0.3.9") || installed(t, dest) != "herdr-recall v0.3.9" {
		t.Fatalf("downgrade permitido: %v %s", err, out)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dest, ".*.new.*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporários ficaram: %v", leftovers)
	}
}

func TestInstallerRefusesUnknownVersionWithoutForce(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "bin")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "herdr-recall"), fakeBinary("herdr-recall", ""), 0o755); err != nil {
		t.Fatal(err)
	}
	writeBundle(t, filepath.Join(root, "pkg"), "v0.4.0")
	out, err := runInstaller(t, filepath.Join(root, "pkg"), dest)
	if err == nil || !strings.Contains(out, "versão desconhecida") || !strings.Contains(out, "--force") || installed(t, dest) != "herdr-recall" {
		t.Fatalf("desconhecida: %v %s", err, out)
	}
	out, err = runInstaller(t, filepath.Join(root, "pkg"), dest, "--force")
	if err != nil || installed(t, dest) != "herdr-recall v0.4.0" {
		t.Fatalf("force: %v %s", err, out)
	}
}

func TestInstallerRefusesPackageWithoutVersion(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, filepath.Join(root, "pkg"), "")
	dest := filepath.Join(root, "bin")
	out, err := runInstaller(t, filepath.Join(root, "pkg"), dest)
	if err == nil || !strings.Contains(out, "não informa a versão") {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("criou o destino")
	}
}

func TestInstallerMentionsServiceRestartAfterUpgrade(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	unit := filepath.Join(home, ".config", "systemd", "user", "herdr-recall.service")
	if err := os.MkdirAll(filepath.Dir(unit), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("[Service]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "bin")
	for _, v := range []string{"v0.4.0", "v0.4.1"} {
		writeBundle(t, filepath.Join(root, v), v)
		cmd := exec.Command("sh", filepath.Join(root, v, "install.sh"), "--bin-dir", dest)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v %s", v, err, out)
		}
		if v == "v0.4.1" && !strings.Contains(string(out), "systemctl --user restart herdr-recall") {
			t.Fatalf("sem aviso de reinício: %s", out)
		}
	}
}
