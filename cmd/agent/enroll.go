package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"herdr-recall/internal/agent"
)

var errUsage = errors.New("uso")

func enroll(args []string, out, errW io.Writer, paths agent.Paths, client *http.Client, unit bool) error {
	flags := flag.NewFlagSet("enroll", flag.ContinueOnError)
	flags.SetOutput(errW)
	api := flags.String("api", "", "URL do servidor")
	code := flags.String("code", "", "código de uso único")
	name := flags.String("name", "", "nome da máquina")
	flags.Usage = func() {
		fmt.Fprintln(errW, "uso: herdr-recall enroll --api URL --code CODIGO --name NOME")
	}
	if err := flags.Parse(args); err != nil {
		return errUsage
	}
	if *api == "" || *code == "" || *name == "" || len(flags.Args()) > 0 {
		flags.Usage()
		return errUsage
	}
	return exchange(out, errW, paths, *api, *code, *name, client, unit)
}

func exchange(out, errW io.Writer, paths agent.Paths, api string, code string, name string, client *http.Client, unit bool) error {
	if _, err := agent.Load(paths.Config); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	replaced := tokenExists(paths.Token)
	result, err := agent.Enroll(context.Background(), client, api, code, name)
	if err != nil {
		return err
	}
	canonical, err := agent.CanonicalAPI(api)
	if err != nil {
		return err
	}
	if err := agent.Save(paths, canonical, result.Token, nil); err != nil {
		return err
	}
	fmt.Fprintln(out, "Máquina enrolada.")
	if replaced {
		fmt.Fprintln(out, "O token anterior foi substituído. A fila local foi conservada.")
	}
	if unit {
		fmt.Fprintln(errW, "A unidade instalada segue com a configuração antiga até herdr-recall service install.")
	}
	return nil
}

func tokenExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Size() > 0
}

func enrollMessage(err error) string {
	var refused *agent.Refusal
	if errors.As(err, &refused) {
		switch refused.Code {
		case "ENROLLMENT_INVALID":
			return "Código de enrolamento inválido."
		case "ENROLLMENT_USED":
			return "Este código já foi usado."
		case "ENROLLMENT_EXPIRED":
			return "Este código expirou."
		case "INVALID_PARAMS":
			if refused.Field == "name" {
				return "Nome inválido."
			}
			if refused.Field == "code" {
				return "Código inválido."
			}
		}
	}
	if errors.Is(err, agent.ErrAPI) {
		return "URL inválida."
	}
	return "Não foi possível enrolar esta máquina."
}

func unitInstalled(home string, goos string) bool {
	var path string
	switch goos {
	case "darwin":
		path = filepath.Join(home, "Library", "LaunchAgents", "dev.herdr-recall.agent.plist")
	case "windows":
		return false
	default:
		path = filepath.Join(home, ".config", "systemd", "user", "herdr-recall.service")
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" && err != nil {
		return "", err
	}
	return trimmed, nil
}
