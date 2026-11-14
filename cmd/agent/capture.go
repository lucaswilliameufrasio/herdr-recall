package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"herdr-recall/internal/agent"
)

func captureCommand(args []string, out, errW io.Writer, opt agent.CaptureOptions) int {
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	flags.SetOutput(errW)
	flags.Usage = func() {
		fmt.Fprintln(errW, "uso: herdr-recall capture")
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) > 0 {
		flags.Usage()
		return 2
	}
	if opt.StateDir == "" || opt.BootID == "" || opt.Host == "" {
		fmt.Fprintln(errW, "Não foi possível gravar a captura.")
		return 1
	}
	result, err := agent.Capture(context.Background(), opt)
	if err != nil || !result.HerdrOK {
		agent.LogCapture(errW, result, err)
		return 1
	}
	fmt.Fprintln(out, "Captura gravada.")
	agent.LogCapture(errW, result, nil)
	return 0
}

func deskOptions(home string) (agent.CaptureOptions, error) {
	bootID, err := agent.CurrentBootID()
	if err != nil {
		return agent.CaptureOptions{}, err
	}
	host, err := agent.CurrentHost()
	if err != nil {
		return agent.CaptureOptions{}, err
	}
	if bootID == "" || host == "" {
		return agent.CaptureOptions{}, errors.New("identidade vazia")
	}
	return agent.CaptureOptions{
		StateDir: filepath.Join(home, ".local", "state", "herdr-recall"),
		Host:     host,
		BootID:   bootID,
	}, nil
}
