package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"herdr-recall/internal/release"
)

func script(line string) []byte { return []byte("#!/bin/sh\necho '" + line + "'\n") }

type updateRig struct {
	home, bin string
	restarts  []string
}

func newUpdateRig(t *testing.T, installed, latest string, reported string) *updateRig {
	t.Helper()
	rig := &updateRig{home: t.TempDir(), bin: t.TempDir()}
	for _, name := range release.Binaries {
		if err := os.WriteFile(filepath.Join(rig.bin, name), script(name+" "+installed), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	for _, name := range release.Binaries {
		body := script(name + " " + reported)
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write(body)
	}
	tw.Close()
	gz.Close()
	sum := sha256.Sum256(archive.Bytes())
	asset := "herdr-recall-" + latest + "-linux-amd64.tar.gz"
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+latest, http.StatusFound)
	})
	mux.HandleFunc("/releases/download/"+latest+"/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%x  %s\n", sum, asset)
	})
	mux.HandleFunc("/releases/download/"+latest+"/"+asset, func(w http.ResponseWriter, r *http.Request) { w.Write(archive.Bytes()) })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	saved := []any{version, releaseClient, executablePath, runQuiet, targetPlatform}
	t.Cleanup(func() {
		version = saved[0].(string)
		releaseClient = saved[1].(func(*env) release.Client)
		executablePath = saved[2].(func() (string, error))
		runQuiet = saved[3].(func(context.Context, string, ...string) error)
		targetPlatform = saved[4].(func() (string, string))
	})
	version = installed
	releaseClient = func(*env) release.Client { return release.Client{HTTP: server.Client(), Repo: server.URL} }
	executablePath = func() (string, error) { return filepath.Join(rig.bin, "herdr-recall"), nil }
	runQuiet = func(_ context.Context, name string, args ...string) error {
		rig.restarts = append(rig.restarts, name+" "+strings.Join(args, " "))
		return nil
	}
	targetPlatform = func() (string, string) { return "linux", "amd64" }
	return rig
}

func (r *updateRig) installed(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command(filepath.Join(r.bin, name)).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func TestUpdateUpgradesBothBinariesAndRestartsOnRequest(t *testing.T) {
	rig := newUpdateRig(t, "v0.4.0", "v0.5.0", "v0.5.0")
	unit := unitPath(rig.home, "linux")
	os.MkdirAll(filepath.Dir(unit), 0o700)
	os.WriteFile(unit, []byte("[Service]\nExecStart="+filepath.Join(rig.bin, "herdr-recall")+" run\n"), 0o600)

	code, out, _ := runCLI(t, rig.home, "update", "--check")
	if code != 0 || !strings.Contains(out, "disponível: v0.5.0") || rig.installed(t, "herdr-recall") != "herdr-recall v0.4.0" {
		t.Fatalf("check: %d %q", code, out)
	}
	code, out, errW := runCLI(t, rig.home, "update", "--restart")
	if code != 0 || !strings.Contains(out, "Atualizado de v0.4.0 para v0.5.0") || !strings.Contains(out, "Serviço reiniciado") {
		t.Fatalf("update: %d %q %q", code, out, errW)
	}
	for _, name := range release.Binaries {
		if got := rig.installed(t, name); got != name+" v0.5.0" {
			t.Fatalf("%s: %q", name, got)
		}
	}
	if len(rig.restarts) != 1 || rig.restarts[0] != "systemctl --user restart herdr-recall.service" {
		t.Fatalf("restart: %v", rig.restarts)
	}
}

func TestUpdateSameDowngradeAndUnknown(t *testing.T) {
	rig := newUpdateRig(t, "v0.5.0", "v0.5.0", "v0.5.0")
	if code, out, _ := runCLI(t, rig.home, "update"); code != 0 || !strings.Contains(out, "Já está na v0.5.0") {
		t.Fatalf("same: %d %q", code, out)
	}

	rig = newUpdateRig(t, "v0.6.0", "v0.5.0", "v0.5.0")
	code, _, errW := runCLI(t, rig.home, "update")
	if code != 1 || !strings.Contains(errW, "--allow-downgrade") || rig.installed(t, "herdr-recall") != "herdr-recall v0.6.0" {
		t.Fatalf("downgrade recusado: %d %q", code, errW)
	}
	code, out, _ := runCLI(t, rig.home, "update", "--allow-downgrade")
	if code != 0 || !strings.Contains(out, "Voltou de v0.6.0 para v0.5.0") || rig.installed(t, "herdr-recall") != "herdr-recall v0.5.0" {
		t.Fatalf("downgrade aceito: %d %q", code, out)
	}

	rig = newUpdateRig(t, "dev", "v0.5.0", "v0.5.0")
	code, _, errW = runCLI(t, rig.home, "update")
	if code != 1 || !strings.Contains(errW, "--force") {
		t.Fatalf("dev: %d %q", code, errW)
	}
	if code, _, _ := runCLI(t, rig.home, "update", "--force"); code != 0 || rig.installed(t, "herdr-recall") != "herdr-recall v0.5.0" {
		t.Fatalf("force: %d", code)
	}
}

func TestUpdateRefusesPackageReportingAnotherVersion(t *testing.T) {
	rig := newUpdateRig(t, "v0.4.0", "v0.5.0", "v0.4.9")
	code, _, errW := runCLI(t, rig.home, "update")
	if code != 1 || !strings.Contains(errW, "não informa a versão v0.5.0") || rig.installed(t, "herdr-recall") != "herdr-recall v0.4.0" {
		t.Fatalf("%d %q", code, errW)
	}
	if code, _, _ := runCLI(t, rig.home, "update", "--tag", "0.5"); code != 2 {
		t.Fatalf("tag inválida: %d", code)
	}
}

func TestUpdateRefusesTemporaryBinary(t *testing.T) {
	rig := newUpdateRig(t, "v0.4.0", "v0.5.0", "v0.5.0")
	executablePath = func() (string, error) { return "/tmp/go-build123/b001/exe/agent", nil }
	if code, _, errW := runCLI(t, rig.home, "update"); code != 1 || !strings.Contains(errW, "não é uma instalação") {
		t.Fatalf("%d %q", code, errW)
	}
}

func TestUpdateLeavesAServiceOfAnotherBinaryAlone(t *testing.T) {
	rig := newUpdateRig(t, "v0.4.0", "v0.5.0", "v0.5.0")
	unit := unitPath(rig.home, "linux")
	os.MkdirAll(filepath.Dir(unit), 0o700)
	os.WriteFile(unit, []byte("[Service]\nExecStart=/outro/lugar/herdr-recall run\n"), 0o600)
	code, out, _ := runCLI(t, rig.home, "update", "--restart")
	if code != 0 || !strings.Contains(out, "usa outro binário") || len(rig.restarts) != 0 {
		t.Fatalf("%d %q %v", code, out, rig.restarts)
	}
}
