package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"video-production-console/internal/catalogbuilder"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	listen, configPath, err := parseOptions(args)
	if err != nil {
		return err
	}
	server, err := catalogbuilder.NewServer(configPath)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              listen,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stdout, "素材建库工作台：http://%s\n", listen)
		errCh <- httpServer.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if err == nil || err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdown)
	}
}

func parseOptions(args []string) (string, string, error) {
	set := flag.NewFlagSet("catalog-builder", flag.ContinueOnError)
	listen := set.String("listen", catalogbuilderListenDefault, "loopback address")
	config := set.String("config", "catalog-builder.config.json", "local config path")
	if err := set.Parse(args); err != nil {
		return "", "", err
	}
	if err := catalogbuilder.ValidateListen(*listen); err != nil {
		return "", "", err
	}
	configPath, err := filepath.Abs(*config)
	if err != nil {
		return "", "", err
	}
	return *listen, configPath, nil
}

const catalogbuilderListenDefault = "127.0.0.1:2031"
