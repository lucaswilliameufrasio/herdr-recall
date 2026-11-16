package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallerInstallsAndRefusesOverwrite(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	script, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bundle := filepath.Join(root, "bundle")
	if err := os.Mkdir(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "install.sh"), script, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"herdr-recall", "herdr-recall-server"} {
		if err := os.WriteFile(filepath.Join(bundle, name), []byte("synthetic-binary"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(root, "bin")
	run := func() ([]byte, error) {
		return exec.Command("sh", filepath.Join(bundle, "install.sh"), "--bin-dir", dest).CombinedOutput()
	}
	if output, err := run(); err != nil {
		t.Fatalf("instalação falhou: %s", output)
	}
	for _, name := range []string{"herdr-recall", "herdr-recall-server"} {
		info, err := os.Stat(filepath.Join(dest, name))
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatal("binário ausente ou sem permissão de execução")
		}
	}
	output, err := run()
	if err == nil || !strings.Contains(string(output), "já existe") {
		t.Fatal("instalador substituiu destino existente")
	}
}
