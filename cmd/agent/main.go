package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"herdr-recall/internal/agent"
)

func main() {
	home, homeErr := os.UserHomeDir()
	e := &env{
		in:      os.Stdin,
		out:     os.Stdout,
		errW:    os.Stderr,
		home:    home,
		homeErr: homeErr,
		goos:    goosName(),
		tty:     isTerminal(os.Stdin) && isTerminal(os.Stdout),
	}
	os.Exit(dispatchEnv(e, os.Args[1:]))
}

var herdrRun agent.Runner = runHerdr

func runHerdr(ctx context.Context, argv []string) ([]byte, error) {
	if len(argv) == 0 || argv[0] != "herdr" {
		return nil, errors.New("comando vazio")
	}
	ctx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = nil
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}
