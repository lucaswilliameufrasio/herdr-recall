// Package release finds, verifies and installs herdr-recall releases.
package release

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DefaultRepo is where releases are published.
const DefaultRepo = "https://github.com/lucaswilliameufrasio/herdr-recall"

// Binaries are the files every release archive carries.
var Binaries = []string{"herdr-recall", "herdr-recall-server"}

const maxArchive = 200 << 20

var (
	ErrNoVersion   = errors.New("versão inválida")
	ErrChecksum    = errors.New("checksum não confere")
	ErrNoAsset     = errors.New("pacote ausente na release")
	ErrLatest      = errors.New("não foi possível descobrir a última release")
	ErrUnsupported = errors.New("plataforma sem pacote")
)

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Version is a release tag vMAJOR.MINOR.PATCH.
type Version struct {
	Major, Minor, Patch int
}

func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Parse accepts only vX.Y.Z; "dev" or anything else is not a release.
func Parse(raw string) (Version, bool) {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return Version{}, false
	}
	var parts [3]int
	for i := range parts {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, false
		}
		parts[i] = n
	}
	return Version{parts[0], parts[1], parts[2]}, true
}

// Compare returns -1, 0 or 1.
func Compare(a, b Version) int {
	for _, pair := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

// Decision is what installing a candidate over the current binary means.
type Decision int

const (
	Upgrade Decision = iota
	Same
	Downgrade
	Unknown
)

// Decide compares the installed version string with a candidate release.
func Decide(installed string, candidate Version) Decision {
	current, ok := Parse(installed)
	if !ok {
		return Unknown
	}
	switch Compare(candidate, current) {
	case 1:
		return Upgrade
	case 0:
		return Same
	default:
		return Downgrade
	}
}

// Client talks to the release host. Repo is overridable for tests.
type Client struct {
	HTTP *http.Client
	Repo string
}

func (c Client) repo() string {
	if c.Repo != "" {
		return strings.TrimRight(c.Repo, "/")
	}
	return DefaultRepo
}

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// Latest reads the tag from the redirect of /releases/latest, which avoids the
// rate-limited API.
func (c Client) Latest(ctx context.Context) (Version, error) {
	client := *c.http()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, c.repo()+"/releases/latest", nil)
	if err != nil {
		return Version{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return Version{}, fmt.Errorf("%w: %v", ErrLatest, err)
	}
	response.Body.Close()
	location := response.Header.Get("Location")
	if response.StatusCode < 300 || response.StatusCode >= 400 || location == "" {
		return Version{}, fmt.Errorf("%w: resposta %d", ErrLatest, response.StatusCode)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		return Version{}, fmt.Errorf("%w: %v", ErrLatest, err)
	}
	v, ok := Parse(path.Base(parsed.Path))
	if !ok {
		return Version{}, fmt.Errorf("%w: tag %q", ErrLatest, path.Base(parsed.Path))
	}
	return v, nil
}

// ArchiveName is the asset for one platform.
func ArchiveName(v Version, goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64":
		return fmt.Sprintf("herdr-recall-%s-%s-%s.tar.gz", v, goos, goarch), nil
	default:
		return "", ErrUnsupported
	}
}

// Download fetches SHA256SUMS and the archive, verifies the digest and
// extracts the binaries into dir (which must exist and be private).
func (c Client) Download(ctx context.Context, v Version, goos, goarch, dir string) error {
	name, err := ArchiveName(v, goos, goarch)
	if err != nil {
		return err
	}
	base := c.repo() + "/releases/download/" + v.String() + "/"
	sums, err := c.fetch(ctx, base+"SHA256SUMS", 1<<20)
	if err != nil {
		return err
	}
	expected, err := digestFor(sums, name)
	if err != nil {
		return err
	}
	archive, err := c.fetch(ctx, base+name, maxArchive)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(archive)
	if hex.EncodeToString(sum[:]) != expected {
		return ErrChecksum
	}
	return extract(archive, dir)
}

func (c Client) fetch(ctx context.Context, target string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", ErrNoAsset, path.Base(target))
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download de %s: resposta %d", path.Base(target), response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s maior que o esperado", path.Base(target))
	}
	return body, nil
}

func digestFor(sums []byte, name string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			digest := strings.ToLower(fields[0])
			if len(digest) != 64 {
				return "", ErrChecksum
			}
			if _, err := hex.DecodeString(digest); err != nil {
				return "", ErrChecksum
			}
			return digest, nil
		}
	}
	return "", fmt.Errorf("%w: %s sem checksum", ErrNoAsset, name)
}

func extract(archive []byte, dir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	defer gz.Close()
	want := map[string]bool{}
	for _, name := range Binaries {
		want[name] = true
	}
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		header.Name = strings.TrimPrefix(header.Name, "./")
		if !want[header.Name] || header.Typeflag != tar.TypeReg {
			continue
		}
		file, err := os.OpenFile(filepath.Join(dir, header.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, io.LimitReader(tr, maxArchive))
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		delete(want, header.Name)
	}
	for name := range want {
		return fmt.Errorf("%w: %s fora do pacote", ErrNoAsset, name)
	}
	return nil
}

// Replace moves the staged binaries over the ones in binDir. Each rename is
// atomic; a running process keeps its old file. The agent goes last so a
// failure leaves the command that can retry intact. Only names in replace are
// touched.
func Replace(staged, binDir string, replace []string) error {
	ordered := make([]string, 0, len(replace))
	for _, name := range replace {
		if name != "herdr-recall" {
			ordered = append(ordered, name)
		}
	}
	for _, name := range replace {
		if name == "herdr-recall" {
			ordered = append(ordered, name)
		}
	}
	var temps []string
	defer func() {
		for _, temp := range temps {
			os.Remove(temp)
		}
	}()
	for _, name := range ordered {
		source, err := os.Open(filepath.Join(staged, name))
		if err != nil {
			return err
		}
		temp, err := os.CreateTemp(binDir, "."+name+".new.*")
		if err != nil {
			source.Close()
			return err
		}
		temps = append(temps, temp.Name())
		_, copyErr := io.Copy(temp, source)
		source.Close()
		syncErr := temp.Sync()
		closeErr := temp.Close()
		if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
			return err
		}
		if err := os.Chmod(temp.Name(), 0o755); err != nil {
			return err
		}
	}
	for i, name := range ordered {
		if err := os.Rename(temps[i], filepath.Join(binDir, name)); err != nil {
			return fmt.Errorf("troca de %s: %w", name, err)
		}
		temps[i] = ""
	}
	return nil
}
