package agent

import (
	"strings"
	"testing"
)

func TestLinuxBootIDTrimsTheLine(t *testing.T) {
	id, err := linuxBootID("11111111-2222-3333-4444-555555555555\n")
	if err != nil || id != "11111111-2222-3333-4444-555555555555" {
		t.Fatal(err)
	}
	if _, err := linuxBootID(" \n"); err == nil {
		t.Fatal("aceitou vazio")
	}
	if _, err := linuxBootID("boot id"); err == nil {
		t.Fatal("aceitou espaço")
	}
}

func TestHashBootIgnoresTheTrailingLine(t *testing.T) {
	first, err := hashBoot([]byte("{ sec = 1, usec = 2 }\n"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := hashBoot([]byte("{ sec = 1, usec = 2 }"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 64 {
		t.Fatal("hash instável")
	}
	if _, err := hashBoot([]byte(" \n")); err == nil {
		t.Fatal("aceitou vazio")
	}
}

func TestCurrentBootIDReadsTheKernel(t *testing.T) {
	id, err := CurrentBootID()
	if err != nil || id == "" || strings.ContainsAny(id, " \r\n") {
		t.Fatal("boot ilegível")
	}
}

func TestCurrentHostIsPresent(t *testing.T) {
	host, err := CurrentHost()
	if err != nil || host == "" {
		t.Fatal("host ilegível")
	}
}
