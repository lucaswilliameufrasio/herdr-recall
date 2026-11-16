package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
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

// RunConnected captures locally before scheduling each bounded delivery turn.
// The credentials are read once at startup; reinstall/restart adopts new ones.
func RunConnected(ctx context.Context, opt CaptureOptions, home string, every time.Duration, log io.Writer) error {
	manager := Manager{Home: home}
	api, token, err := manager.serviceCredentials()
	if err != nil {
		return err
	}
	return runDelivery(ctx, opt, every, log, deliveryOptions{
		StateDir: opt.StateDir,
		API:      api,
		Token:    token,
		GOOS:     opt.GOOS,
		DirSync:  opt.DirSync,
	})
}

func runDelivery(ctx context.Context, opt CaptureOptions, every time.Duration, log io.Writer, delivery deliveryOptions) error {
	if every <= 0 {
		every = CaptureEvery
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	jobs := make(chan context.Context, 1)
	revoked := make(chan struct{}, 1)
	var worker sync.WaitGroup
	var logMu sync.Mutex
	worker.Add(1)
	go func() {
		defer worker.Done()
		for {
			select {
			case <-runCtx.Done():
				return
			case job := <-jobs:
				err := drainQueue(job, delivery)
				if errors.Is(err, ErrDeviceRevoked) {
					select {
					case revoked <- struct{}{}:
					default:
					}
					stop()
					return
				}
				if err != nil && job.Err() == nil && log != nil {
					logMu.Lock()
					fmt.Fprintln(log, "Envio adiado. A fila permanece na máquina.")
					logMu.Unlock()
				}
			}
		}
	}()
	defer worker.Wait()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	var cancelTurn context.CancelFunc
	defer func() {
		if cancelTurn != nil {
			cancelTurn()
		}
		stop()
	}()
	for {
		if cancelTurn != nil {
			cancelTurn()
		}
		if runCtx.Err() != nil {
			break
		}
		result, err := Capture(runCtx, opt)
		if runCtx.Err() != nil {
			break
		}
		logMu.Lock()
		LogCapture(log, result, err)
		logMu.Unlock()
		if err == nil {
			turn, cancel := context.WithCancel(runCtx)
			cancelTurn = cancel
			select {
			case jobs <- turn:
			default:
				select {
				case <-jobs:
				default:
				}
				jobs <- turn
			}
		}
		select {
		case <-runCtx.Done():
		case <-ticker.C:
		}
	}
	select {
	case <-revoked:
		return ErrDeviceRevoked
	default:
		return nil
	}
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
