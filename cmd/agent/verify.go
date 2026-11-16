package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"herdr-recall/internal/agent"
)

func verifyCommand(args []string, out, errW io.Writer, home, goos string) int {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(errW)
	send := flags.Bool("send", false, "grava uma captura real e envia a fila até ela")
	flags.Usage = func() {
		fmt.Fprintln(errW, "uso: herdr-recall verify [--send]")
		fmt.Fprintln(errW, "--send grava uma captura real e envia também os itens anteriores da fila.")
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) != 0 {
		flags.Usage()
		return 2
	}
	if !*send {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintln(errW, "Não foi possível verificar esta máquina.")
			return 1
		}
		fmt.Fprintln(out, "Diagnóstico sem captura nem envio. Para testar uma captura real: herdr-recall verify --send")
		fmt.Fprintln(out, "O diagnóstico não comprova entrega de captura nem restauração.")
		manager := agent.NewManager(goos, home, exe, os.Getenv("USER"), os.Getuid())
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		checks := manager.Doctor(ctx)
		for _, check := range checks {
			if check.Name == "herdr" && check.Status == agent.StatusOK {
				check.Detail = "disponível no PATH da unidade"
			}
			if check.Name == "serviço" && check.Status == agent.StatusFail {
				check.Detail = "não está em execução ou não foi possível verificar"
			}
			if check.Name == "linger" {
				check.Fix = ""
			}
			if check.Fix != "" {
				fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", check.Status, check.Name, check.Detail, check.Fix)
			} else {
				fmt.Fprintf(out, "%s\t%s\t%s\n", check.Status, check.Name, check.Detail)
			}
		}
		if agent.Failed(checks) {
			fmt.Fprintln(errW, "Verificação encontrou falhas.")
			return 1
		}
		fmt.Fprintln(out, "Diagnóstico concluído; avisos não são falhas.")
		return 0
	}
	fmt.Fprintln(out, "Verificação ativa: grava uma captura real e envia a fila anterior até ela. O servidor pode armazenar esses dados.")
	opt, err := deskOptions(home)
	if err != nil {
		fmt.Fprintln(errW, "Não foi possível preparar a captura.")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := agent.VerifyDelivery(ctx, home, opt); err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	fmt.Fprintln(out, "Captura gravada e confirmada pelo servidor. Painel e restauração não foram verificados.")
	return 0
}
