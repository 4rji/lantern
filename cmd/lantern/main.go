// Command lantern serves a local web dashboard for the content QMD knows about.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/4rji/lantern/internal/config"
	"github.com/4rji/lantern/internal/qmd"
	"github.com/4rji/lantern/internal/server"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "lantern:", err)
		os.Exit(1)
	}
}

func run() error {
	home, _ := os.UserHomeDir()
	cfg, err := config.Parse(os.Args[1:], os.Getenv, home, os.Stderr)
	if err != nil {
		return err
	}
	if cfg.Version {
		fmt.Println("lantern", version)
		return nil
	}

	srv, err := server.New(server.Options{
		Version:       version,
		QMDURL:        cfg.QMDURL,
		QMDConfigPath: cfg.QMDConfigPath,
		LocalOnly:     cfg.Loopback(),
	}, qmd.New(cfg.QMDURL))
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	httpSrv := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()

	fmt.Printf("lantern running at %s\n", cfg.URL())
	fmt.Printf("  QMD server: %s\n  QMD config: %s\n", cfg.QMDURL, cfg.QMDConfigPath)
	if !cfg.Loopback() {
		fmt.Println("  warning: listening beyond localhost with no auth; anyone who can reach this address can read your notes")
	}
	if cfg.Open {
		if err := openBrowser(cfg.URL()); err != nil {
			log.Printf("could not open the browser: %v", err)
		}
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	stop()
	fmt.Println("\nshutting down…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("don't know how to open a browser on %s", runtime.GOOS)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap the process
	return nil
}
