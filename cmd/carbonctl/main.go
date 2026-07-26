package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/server"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/state"
)

func main() {
	var listenAddress string
	var printerURL string
	var statePath string
	flag.StringVar(&listenAddress, "listen", "127.0.0.1:9086", "loopback HTTP listen address")
	flag.StringVar(&printerURL, "printer-url", "", "OpenCentauri WebSocket URL")
	flag.StringVar(&statePath, "state", "", "persistent state document path")
	flag.Parse()

	if err := run(listenAddress, printerURL, statePath); err != nil {
		slog.Error("carbonctl stopped", "error", err)
		os.Exit(1)
	}
}

func run(listenAddress, printerURL, statePath string) error {
	host, _, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return server.ErrNonLoopback
	}

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return err
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := state.Open(statePath)
	if err != nil {
		return err
	}
	go (printer.Client{URL: printerURL, Observer: store}).Run(ctx)
	service := &http.Server{Handler: server.New(store), ReadHeaderTimeout: server.HeaderTimeout}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), server.ShutdownTimeout)
		defer cancel()
		_ = service.Shutdown(shutdownCtx)
	}()
	return service.Serve(listener)
}
