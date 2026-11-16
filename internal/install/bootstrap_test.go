package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBootstrapVerifiesBeforeInstalling(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprintf("corrupt=%v", corrupt), func(t *testing.T) {
			_, source, _, _ := runtime.Caller(0)
			repo := filepath.Join(filepath.Dir(source), "..", "..")
			installer, err := os.ReadFile(filepath.Join(repo, "install.sh"))
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			fixture := filepath.Join(root, "fixture")
			bin := filepath.Join(root, "tools")
			dest := filepath.Join(root, "installed")
			for _, dir := range []string{fixture, bin} {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var archive bytes.Buffer
			gz := gzip.NewWriter(&archive)
			tw := tar.NewWriter(gz)
			for name, content := range map[string][]byte{
				"herdr-recall": fakeBinary("herdr-recall", "v9.8.7"), "herdr-recall-server": fakeBinary("herdr-recall-server", "v9.8.7"), "install.sh": installer,
			} {
				if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
					t.Fatal(err)
				}
				if _, err := tw.Write(content); err != nil {
					t.Fatal(err)
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			filename := "herdr-recall-v9.8.7-linux-amd64.tar.gz"
			sum := sha256.Sum256(archive.Bytes())
			if err := os.WriteFile(filepath.Join(fixture, filename), archive.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			if corrupt {
				sum[0] ^= 1
			}
			if err := os.WriteFile(filepath.Join(fixture, "SHA256SUMS"), []byte(fmt.Sprintf("%x  %s\n", sum, filename)), 0o600); err != nil {
				t.Fatal(err)
			}
			curl := "#!/bin/sh\nout=''\nurl=''\nwhile [ \"$#\" -gt 0 ]; do case \"$1\" in -o) out=$2; shift 2;; https://*) url=$1; shift;; *) shift;; esac; done\ncp \"$FIXTURE_DIR/${url##*/}\" \"$out\"\n"
			uname := "#!/bin/sh\ncase \"$1\" in -s) echo Linux;; -m) echo x86_64;; esac\n"
			for name, content := range map[string]string{"curl": curl, "uname": uname} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("sh", filepath.Join(repo, "scripts", "herdr-recall-installer.sh"), "--tag", "v9.8.7", "--bin-dir", dest)
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FIXTURE_DIR="+fixture)
			output, err := cmd.CombinedOutput()
			if corrupt {
				if err == nil || !strings.Contains(string(output), "Checksum não confere") {
					t.Fatal("pacote adulterado não foi recusado")
				}
				if _, err := os.Stat(dest); !os.IsNotExist(err) {
					t.Fatal("pacote adulterado criou o destino")
				}
				return
			}
			if err != nil {
				t.Fatalf("instalação falhou: %s", output)
			}
			for _, name := range []string{"herdr-recall", "herdr-recall-server"} {
				if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
					t.Fatal("binário ausente")
				}
			}
		})
	}
}
