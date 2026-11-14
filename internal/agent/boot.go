package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// CurrentBootID reads the kernel boot identifier. Linux uses boot_id.
// macOS uses the SHA-256 of kern.boottime. The value identifies a boot.
// It does not order boots.
func CurrentBootID() (string, error) {
	switch runtime.GOOS {
	case "linux":
		raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
		if err != nil {
			return "", err
		}
		return linuxBootID(string(raw))
	case "darwin":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sysctl", "-n", "kern.boottime")
		cmd.Stdin = nil
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		return hashBoot(out)
	default:
		return "", errors.New("boot sem suporte")
	}
}

func linuxBootID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" || strings.ContainsAny(id, " \r\n") {
		return "", errors.New("boot inválido")
	}
	return id, nil
}

func hashBoot(raw []byte) (string, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", errors.New("boot inválido")
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:]), nil
}

// CurrentHost is the machine name stored on the snapshot.
func CurrentHost() (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", err
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", errors.New("host vazio")
	}
	return host, nil
}
