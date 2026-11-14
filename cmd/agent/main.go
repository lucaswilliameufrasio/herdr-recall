package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"herdr-recall/internal/agent"
)

func main() {
	home, homeErr := os.UserHomeDir()
	os.Exit(dispatch(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, home, homeErr, nil, runtime.GOOS))
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

func dispatch(args []string, in io.Reader, out, errW io.Writer, home string, homeErr error, client *http.Client, goos string) int {
	if len(args) == 0 {
		fmt.Fprintln(out, "herdr-recall")
		return 0
	}
	switch args[0] {
	case "latest":
		if homeErr != nil {
			fmt.Fprintln(errW, "Não foi possível mostrar as capturas.")
			return 1
		}
		path := filepath.Join(home, ".local", "state", "herdr-recall", "latest.json")
		if err := run(args, out, errW, path); err != nil {
			fmt.Fprintln(errW, "Não foi possível mostrar as capturas.")
			return 1
		}
		return 0
	case "setup", "enroll":
		if homeErr != nil {
			fmt.Fprintln(errW, "Não foi possível enrolar esta máquina.")
			return 1
		}
		paths := agent.PathsFor(home)
		unit := unitInstalled(home, goos)
		var err error
		if args[0] == "setup" {
			err = setup(args[1:], in, out, errW, paths, client, unit)
		} else {
			err = enroll(args[1:], out, errW, paths, client, unit)
		}
		if err == nil {
			return 0
		}
		if errorsIsUsage(err) {
			return 2
		}
		fmt.Fprintln(errW, enrollMessage(err))
		return 1
	case "capture":
		if homeErr != nil {
			fmt.Fprintln(errW, "Não foi possível gravar a captura.")
			return 1
		}
		bootID, bootErr := agent.CurrentBootID()
		if bootErr != nil {
			fmt.Fprintln(errW, "Não foi possível gravar a captura.")
			return 1
		}
		host, hostErr := agent.CurrentHost()
		if hostErr != nil {
			fmt.Fprintln(errW, "Não foi possível gravar a captura.")
			return 1
		}
		return captureCommand(args[1:], out, errW, agent.CaptureOptions{
			StateDir: filepath.Join(home, ".local", "state", "herdr-recall"),
			Host:     host,
			BootID:   bootID,
		})
	case "restore":
		if homeErr != nil {
			fmt.Fprintln(errW, "Não foi possível montar a mesa.")
			return 1
		}
		path := filepath.Join(home, ".local", "state", "herdr-recall", "latest.json")
		err := restoreCommand(args[1:], out, errW, path, herdrRun)
		return restoreExit(err)
	default:
		fmt.Fprintln(out, "herdr-recall")
		return 0
	}
}

func errorsIsUsage(err error) bool {
	return err == errUsage
}
