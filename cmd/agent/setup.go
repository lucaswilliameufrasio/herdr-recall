package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"herdr-recall/internal/agent"
)

func setup(args []string, in io.Reader, out, errW io.Writer, paths agent.Paths, client *http.Client, unit bool) error {
	if len(args) > 0 {
		fmt.Fprintln(errW, "uso: herdr-recall setup")
		return errUsage
	}
	saved, err := agent.Load(paths.Config)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	reader := bufio.NewReader(in)
	fmt.Fprintln(out, "URL do servidor:")
	api, err := readLine(reader)
	if err != nil {
		return err
	}
	canonical, err := agent.CanonicalAPI(api)
	if err != nil {
		return err
	}
	if urlChanged(saved.API, canonical, paths.Token) {
		fmt.Fprintln(out, "A URL mudou. É preciso um código novo. A fila local fica.")
	}
	fmt.Fprintln(out, "Código:")
	code, err := readLine(reader)
	if err != nil {
		return err
	}
	if err := agent.ValidateCode(code); err != nil {
		return err
	}
	fmt.Fprintln(out, "Nome da máquina:")
	name, err := readLine(reader)
	if err != nil {
		return err
	}
	if _, err := agent.ValidateName(name); err != nil {
		return err
	}
	return exchange(out, errW, paths, canonical, code, name, client, unit)
}

func urlChanged(saved string, next string, tokenPath string) bool {
	if saved == "" || !tokenExists(tokenPath) {
		return false
	}
	previous, err := agent.CanonicalAPI(saved)
	if err != nil {
		return true
	}
	return previous != next
}
