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
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/server"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/state"
)

func main() {
	var listenAddress string
	var printerURL string
	var statePath string
	var telemetryFreshness time.Duration
	flag.StringVar(&listenAddress, "listen", "127.0.0.1:9086", "loopback HTTP listen address")
	flag.StringVar(&printerURL, "printer-url", "", "OpenCentauri WebSocket URL")
	flag.StringVar(&statePath, "state", "", "persistent state document path")
	flag.DurationVar(&telemetryFreshness, "telemetry-freshness", server.DefaultTelemetryFreshness, "maximum ready telemetry age")
	flag.Parse()

	if err := run(listenAddress, printerURL, statePath, telemetryFreshness); err != nil {
		slog.Error("carbonctl stopped", "error", err)
		os.Exit(1)
	}
}

func run(listenAddress, printerURL, statePath string, telemetryFreshness time.Duration) error {
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
	go func() {
		if err := (printer.Client{URL: printerURL, Observer: store}).Run(ctx); err != nil {
			slog.Error("printer observation stopped", "error", err)
			stop()
		}
	}()
	service := &http.Server{
		Handler:           server.New(store, server.WithTelemetryFreshness(telemetryFreshness)),
		ReadHeaderTimeout: server.HeaderTimeout,
		ReadTimeout:       server.ReadTimeout,
		WriteTimeout:      server.WriteTimeout,
		IdleTimeout:       server.IdleTimeout,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), server.ShutdownTimeout)
		defer cancel()
		_ = service.Shutdown(shutdownCtx)
	}()
	return service.Serve(listener)
}
