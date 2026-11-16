package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"herdr-recall/internal/agent"
	"herdr-recall/internal/release"
)

type uninstallRig struct {
	home, bin string
	removed   int
}

func newUninstallRig(t *testing.T) *uninstallRig {
	t.Helper()
	rig := &uninstallRig{home: t.TempDir(), bin: t.TempDir()}
	for _, name := range release.Binaries {
		os.WriteFile(filepath.Join(rig.bin, name), []byte("x"), 0o755)
	}
	unit := unitPath(rig.home, "linux")
	os.MkdirAll(filepath.Dir(unit), 0o700)
	os.WriteFile(unit, []byte("[Service]\n"), 0o600)
	writeState(t, rig.home, map[string]string{"spool.json": `{"items":[{"kind":"presence","observed_at":"2026-11-13T00:00:00.000000000Z","body":{}}]}`})
	config := filepath.Dir(agent.PathsFor(rig.home).Config)
	os.MkdirAll(config, 0o700)
	os.WriteFile(filepath.Join(config, "agent.json"), []byte("{}"), 0o600)

	prevExe, prevService := executablePath, uninstallService
	t.Cleanup(func() { executablePath, uninstallService = prevExe, prevService })
	executablePath = func() (string, error) { return filepath.Join(rig.bin, "herdr-recall"), nil }
	uninstallService = func(context.Context, *env, string) (agent.Report, error) {
		rig.removed++
		os.Remove(unitPath(rig.home, "linux"))
		return agent.Report{Changed: true}, nil
	}
	return rig
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestUninstallDryRunAndConfirmation(t *testing.T) {
	rig := newUninstallRig(t)
	code, out, _ := runCLI(t, rig.home, "uninstall", "--dry-run", "--purge")
	if code != 0 || !strings.Contains(out, "Nada foi removido.") || !strings.Contains(out, "1 item na fila") || !exists(filepath.Join(rig.bin, "herdr-recall")) || rig.removed != 0 {
		t.Fatalf("dry-run: %d %q", code, out)
	}
	if code, _, errW := runCLI(t, rig.home, "uninstall"); code != 2 || !strings.Contains(errW, "--yes") {
		t.Fatalf("sem tty: %d %q", code, errW)
	}
	var out2, errW bytes.Buffer
	e := &env{in: strings.NewReader("nao\n"), out: &out2, errW: &errW, home: rig.home, goos: "linux", tty: true}
	if code := dispatchEnv(e, []string{"uninstall"}); code != 1 || !strings.Contains(out2.String(), "Cancelado") || !exists(filepath.Join(rig.bin, "herdr-recall")) {
		t.Fatalf("cancelado: %d %q", code, out2.String())
	}
}

func TestUninstallKeepsDataUnlessPurge(t *testing.T) {
	rig := newUninstallRig(t)
	code, out, _ := runCLI(t, rig.home, "uninstall", "--yes")
	if code != 0 || rig.removed != 1 || exists(filepath.Join(rig.bin, "herdr-recall")) || exists(filepath.Join(rig.bin, "herdr-recall-server")) {
		t.Fatalf("remoção: %d %q", code, out)
	}
	if !exists(stateDir(rig.home)) || !exists(agent.PathsFor(rig.home).Config) {
		t.Fatal("apagou dados sem --purge")
	}

	rig = newUninstallRig(t)
	var out2, errW bytes.Buffer
	e := &env{in: strings.NewReader("remover\n"), out: &out2, errW: &errW, home: rig.home, goos: "linux", tty: true}
	if code := dispatchEnv(e, []string{"uninstall", "--purge"}); code != 0 || exists(stateDir(rig.home)) || exists(filepath.Dir(agent.PathsFor(rig.home).Config)) {
		t.Fatalf("purge: %d %q %q", code, out2.String(), errW.String())
	}
}
