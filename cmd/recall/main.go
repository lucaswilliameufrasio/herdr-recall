package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"herdr-recall/internal/server"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Não foi possível iniciar o servidor.")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], home, os.Getenv("RECALL_SETUP_TOKEN"), versionOutput(os.Args[1:]), net.Listen); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		if errors.Is(err, errVersion) {
			return
		}
		fmt.Fprintln(os.Stderr, "Não foi possível iniciar o servidor.")
		os.Exit(1)
	}
}

// Filled by package-release.sh through -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

var errVersion = errors.New("versão")

func run(ctx context.Context, args []string, home, setupToken string, output io.Writer, listen func(string, string) (net.Listener, error)) error {
	flags := flag.NewFlagSet("herdr-recall-server", flag.ContinueOnError)
	flags.SetOutput(output)
	bind := flags.String("bind", "127.0.0.1:8788", "endereço local do servidor")
	database := flags.String("db", filepath.Join(home, ".local", "state", "herdr-recall-server", "server.db"), "arquivo SQLite privado")
	showVersion := flags.Bool("version", false, "mostra a versão e sai")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Fprintln(output, "herdr-recall-server "+version)
		return errVersion
	}
	if len(flags.Args()) != 0 || !loopbackBind(*bind) || *database == "" {
		return errors.New("parâmetros inválidos")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dbPath, err := filepath.Abs(*database)
	if err != nil || strings.ContainsAny(dbPath, "?#%") {
		return errors.New("caminho do banco inválido")
	}
	listener, err := listen("tcp", *bind)
	if err != nil {
		return err
	}
	defer listener.Close()
	if !loopbackBind(listener.Addr().String()) {
		return errors.New("listener não é local")
	}
	if err := prepareDatabase(dbPath); err != nil {
		return err
	}
	store, err := server.Open(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	handler := server.New(store, server.Options{Loopback: true, SetupToken: setupToken}).Handler()
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() {
		done <- httpServer.Serve(listener)
	}()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			_ = httpServer.Close()
			<-done
			return err
		}
		<-done
		return nil
	}
}

func loopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func prepareDatabase(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err == nil {
		return file.Close()
	}
	if !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("arquivo de banco não é privado")
	}
	return nil
}

// versionOutput sends --version to stdout, where scripts read it; everything
// else keeps going to stderr.
func versionOutput(args []string) io.Writer {
	for _, arg := range args {
		if arg == "--version" || arg == "-version" {
			return os.Stdout
		}
	}
	return os.Stderr
}
