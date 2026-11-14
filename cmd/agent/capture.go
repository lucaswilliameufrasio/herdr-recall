package main

import (
	"context"
	"flag"
	"fmt"
	"io"

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
	if err != nil {
		fmt.Fprintln(errW, "Não foi possível gravar a captura.")
		return 1
	}
	if !result.HerdrOK {
		fmt.Fprintln(errW, "O Herdr não respondeu. A última captura continua aqui.")
		fmt.Fprintln(errW, "capture panes=0")
		return 1
	}
	fmt.Fprintln(out, "Captura gravada.")
	fmt.Fprintf(errW, "capture panes=%d layouts=%d\n", result.Panes, result.Layouts)
	return 0
}
