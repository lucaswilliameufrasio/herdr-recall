package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, home string, args ...string) (int, string, string) {
	t.Helper()
	var out, errW bytes.Buffer
	code := dispatch(args, nil, &out, &errW, home, nil, nil, "linux")
	return code, out.String(), errW.String()
}

func TestVersionFlagsPrintParsableLine(t *testing.T) {
	previous := version
	version = "v0.4.0"
	t.Cleanup(func() { version = previous })
	for _, args := range [][]string{{"--version"}, {"-V"}, {"version", "--short"}} {
		code, out, _ := runCLI(t, t.TempDir(), args...)
		if code != 0 || out != "herdr-recall v0.4.0\n" {
			t.Fatalf("%v: code=%d out=%q", args, code, out)
		}
	}
	code, out, _ := runCLI(t, t.TempDir(), "version")
	if code != 0 || !strings.HasPrefix(out, "herdr-recall v0.4.0\n") || !strings.Contains(out, "go ") {
		t.Fatalf("version: code=%d out=%q", code, out)
	}
}

func TestUnknownCommandAndFlagExitTwo(t *testing.T) {
	code, out, errW := runCLI(t, t.TempDir(), "foo")
	if code != 2 || out != "" || !strings.Contains(errW, "Comando desconhecido: foo") {
		t.Fatalf("comando: code=%d out=%q err=%q", code, out, errW)
	}
	code, _, errW = runCLI(t, t.TempDir(), "latest", "--nada")
	if code != 2 || !strings.Contains(errW, "Opção desconhecida: --nada") || !strings.Contains(errW, "herdr-recall latest --help") {
		t.Fatalf("opção: code=%d err=%q", code, errW)
	}
}

func TestRootHelpListsGroupsWithoutTouchingState(t *testing.T) {
	home := t.TempDir()
	code, out, _ := runCLI(t, home, "--help")
	for _, want := range []string{"Comece por aqui", "Começo:", "Mesa:", "Agente e diagnóstico:", "Manutenção:", "restore", "completion"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("falta %q em %q", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".local")); !os.IsNotExist(err) {
		t.Fatal("a ajuda criou estado")
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		code, out, _ := runCLI(t, t.TempDir(), "completion", shell)
		if code != 0 || !strings.Contains(out, "herdr-recall") {
			t.Fatalf("%s: code=%d", shell, code)
		}
	}
	if code, _, _ := runCLI(t, t.TempDir(), "completion", "tcsh"); code != 2 {
		t.Fatalf("tcsh: %d", code)
	}
}
