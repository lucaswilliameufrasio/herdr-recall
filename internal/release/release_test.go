package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseAndDecide(t *testing.T) {
	for _, bad := range []string{"dev", "0.4.0", "v0.4", "v01.2.3", "v1.2.3-rc1", ""} {
		if _, ok := Parse(bad); ok {
			t.Fatalf("aceitou %q", bad)
		}
	}
	v040, _ := Parse("v0.4.0")
	v0100, _ := Parse("v0.10.0")
	cases := []struct {
		installed string
		candidate Version
		want      Decision
	}{
		{"v0.4.0", v0100, Upgrade},
		{"v0.10.0", v040, Downgrade},
		{"v0.4.0", v040, Same},
		{"dev", v040, Unknown},
		{"", v040, Unknown},
	}
	for _, c := range cases {
		if got := Decide(c.installed, c.candidate); got != c.want {
			t.Fatalf("%s → %s: %v", c.installed, c.candidate, got)
		}
	}
}

// FakeRelease serves /releases/latest and /releases/download/TAG/... like GitHub.
func FakeRelease(t *testing.T, tag string, files map[string][]byte, corrupt bool) *httptest.Server {
	t.Helper()
	v, _ := Parse(tag)
	name, _ := ArchiveName(v, "linux", "amd64")
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	for file, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: file, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(content)
	}
	tw.Close()
	gz.Close()
	sum := sha256.Sum256(archive.Bytes())
	if corrupt {
		sum[0] ^= 1
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/download/"+tag+"/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%x  %s\n", sum, name)
	})
	mux.HandleFunc("/releases/download/"+tag+"/"+name, func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive.Bytes())
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestLatestDownloadAndReplace(t *testing.T) {
	server := FakeRelease(t, "v0.5.0", map[string][]byte{"herdr-recall": []byte("agent-new"), "herdr-recall-server": []byte("server-new"), "README.md": []byte("x")}, false)
	client := Client{HTTP: server.Client(), Repo: server.URL}
	v, err := client.Latest(context.Background())
	if err != nil || v.String() != "v0.5.0" {
		t.Fatalf("latest: %v %v", v, err)
	}
	staged := t.TempDir()
	if err := client.Download(context.Background(), v, "linux", "amd64", staged); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "herdr-recall"), []byte("agent-old"), 0o755)
	if err := Replace(staged, bin, []string{"herdr-recall"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(bin, "herdr-recall"))
	info, _ := os.Stat(filepath.Join(bin, "herdr-recall"))
	if string(got) != "agent-new" || info.Mode().Perm() != 0o755 {
		t.Fatalf("troca: %q %v", got, info.Mode())
	}
	if _, err := os.Stat(filepath.Join(bin, "herdr-recall-server")); !os.IsNotExist(err) {
		t.Fatal("instalou o servidor sem pedir")
	}
	entries, _ := os.ReadDir(bin)
	if len(entries) != 1 {
		t.Fatalf("sobras: %v", entries)
	}
}

func TestDownloadRefusesBadChecksumAndMissingBinary(t *testing.T) {
	server := FakeRelease(t, "v0.5.0", map[string][]byte{"herdr-recall": []byte("a"), "herdr-recall-server": []byte("b")}, true)
	client := Client{HTTP: server.Client(), Repo: server.URL}
	v, _ := Parse("v0.5.0")
	if err := client.Download(context.Background(), v, "linux", "amd64", t.TempDir()); !errors.Is(err, ErrChecksum) {
		t.Fatalf("checksum: %v", err)
	}
	server = FakeRelease(t, "v0.5.0", map[string][]byte{"herdr-recall": []byte("a")}, false)
	client = Client{HTTP: server.Client(), Repo: server.URL}
	if err := client.Download(context.Background(), v, "linux", "amd64", t.TempDir()); !errors.Is(err, ErrNoAsset) {
		t.Fatalf("sem servidor: %v", err)
	}
	if err := client.Download(context.Background(), v, "windows", "amd64", t.TempDir()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("windows: %v", err)
	}
	missing, _ := Parse("v9.9.9")
	if err := client.Download(context.Background(), missing, "linux", "amd64", t.TempDir()); !errors.Is(err, ErrNoAsset) {
		t.Fatalf("tag ausente: %v", err)
	}
}
