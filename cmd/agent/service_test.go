package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDispatchRunRejectsExtraArguments(t *testing.T) {
	var out, errW bytes.Buffer
	code := dispatch([]string{"run", "agora"}, nil, &out, &errW, t.TempDir(), nil, nil, "linux")
	if code != 2 || !strings.Contains(errW.String(), "uso: herdr-recall run") {
		t.Fatalf("code=%d err=%q", code, errW.String())
	}
}

func TestDispatchServiceUsageAndWindows(t *testing.T) {
	home := t.TempDir()
	var out, errW bytes.Buffer
	code := dispatch([]string{"service"}, nil, &out, &errW, home, nil, nil, "linux")
	if code != 2 || !strings.Contains(errW.String(), "uso: herdr-recall service") {
		t.Fatalf("code=%d err=%q", code, errW.String())
	}
	out.Reset()
	errW.Reset()
	code = dispatch([]string{"service", "install"}, nil, &out, &errW, home, nil, nil, "windows")
	if code != 1 || !strings.Contains(errW.String(), "Windows") {
		t.Fatalf("code=%d err=%q", code, errW.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", "herdr-recall.service")); !os.IsNotExist(err) {
		t.Fatal("o windows gravou uma unidade")
	}
}
