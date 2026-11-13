package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"herdr-recall/internal/agent"
)

func main() {
	home, homeErr := os.UserHomeDir()
	os.Exit(dispatch(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, home, homeErr, nil, runtime.GOOS))
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
	default:
		fmt.Fprintln(out, "herdr-recall")
		return 0
	}
}

func errorsIsUsage(err error) bool {
	return err == errUsage
}
