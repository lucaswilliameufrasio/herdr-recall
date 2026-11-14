package agent

import (
	"context"
	"fmt"
	"io"
	"time"
)

// CaptureEvery is the monotonic gap between captures.
const CaptureEvery = time.Minute

// LogCapture writes the count for one turn. The line has no argv, session, or directory.
func LogCapture(w io.Writer, result CaptureResult, err error) {
	if w == nil {
		return
	}
	if err != nil {
		fmt.Fprintln(w, "Não foi possível gravar a captura.")
		return
	}
	if !result.HerdrOK {
		fmt.Fprintln(w, "O Herdr não respondeu. A última captura continua aqui.")
		fmt.Fprintln(w, "capture panes=0")
		return
	}
	fmt.Fprintf(w, "capture panes=%d layouts=%d\n", result.Panes, result.Layouts)
}

// Run captures immediately and again on a monotonic interval.
// It does not open a network connection. Cancel ends the loop.
func Run(ctx context.Context, opt CaptureOptions, every time.Duration, log io.Writer) error {
	if every <= 0 {
		every = CaptureEvery
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		result, err := Capture(ctx, opt)
		if ctx.Err() != nil {
			return nil
		}
		LogCapture(log, result, err)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
