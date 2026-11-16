package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"syscall"

	"herdr-recall/internal/agent"
)

func runCommand(args []string, errW io.Writer, opt agent.CaptureOptions, home string) int {
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
	if err := agent.RunConnected(ctx, opt, home, agent.CaptureEvery, errW); err != nil {
		if errors.Is(err, agent.ErrDeviceRevoked) {
			fmt.Fprintln(errW, "Dispositivo recusado ou revogado. A fila permanece na máquina.")
			return 3
		}
		if errors.Is(err, agent.ErrServiceConfig) || errors.Is(err, agent.ErrServiceToken) {
			fmt.Fprintln(errW, err.Error())
			return 1
		}
		fmt.Fprintln(errW, "Não foi possível gravar a captura.")
		return 1
	}
	return 0
}
