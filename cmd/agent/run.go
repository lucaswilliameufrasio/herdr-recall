package main

import (
	"context"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"herdr-recall/internal/agent"
)

func runCommand(args []string, errW io.Writer, opt agent.CaptureOptions) int {
	if len(args) > 0 {
		fmt.Fprintln(errW, "uso: herdr-recall run")
		return 2
	}
	if opt.StateDir == "" || opt.BootID == "" || opt.Host == "" {
		fmt.Fprintln(errW, "Não foi possível gravar a captura.")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := agent.Run(ctx, opt, agent.CaptureEvery, errW); err != nil {
		fmt.Fprintln(errW, "Não foi possível gravar a captura.")
		return 1
	}
	return 0
}
