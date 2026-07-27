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

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/camera"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/control"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/history"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/server"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/snapshots"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/state"
)

type config struct {
	listenAddress      string
	printerURL         string
	statePath          string
	telemetryFreshness time.Duration
	cameraURL          string
	historyURL         string
	snapshotDirectory  string
	snapshotRetention  time.Duration
}

func main() {
	var configuration config
	flag.StringVar(&configuration.listenAddress, "listen", "127.0.0.1:9086", "loopback HTTP listen address")
	flag.StringVar(&configuration.printerURL, "printer-url", "", "OpenCentauri WebSocket URL")
	flag.StringVar(&configuration.statePath, "state", "", "persistent state document path")
	flag.DurationVar(&configuration.telemetryFreshness, "telemetry-freshness", server.DefaultTelemetryFreshness, "maximum ready telemetry age")
	flag.StringVar(&configuration.cameraURL, "camera-url", "", "OpenCentauri MJPEG URL")
	flag.StringVar(&configuration.historyURL, "history-url", "", "printer history URL")
	flag.StringVar(&configuration.snapshotDirectory, "snapshot-dir", "", "retained snapshot directory")
	flag.DurationVar(&configuration.snapshotRetention, "snapshot-retention", 72*time.Hour, "retained snapshot lifetime")
	flag.Parse()

	if err := run(configuration); err != nil {
		slog.Error("carbonctl stopped", "error", err)
		os.Exit(1)
	}
}

func run(configuration config) error {
	host, _, err := net.SplitHostPort(configuration.listenAddress)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return server.ErrNonLoopback
	}

	listener, err := net.Listen("tcp", configuration.listenAddress)
	if err != nil {
		return err
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := state.Open(configuration.statePath)
	if err != nil {
		return err
	}
	go func() {
		if err := (printer.Client{URL: configuration.printerURL, Observer: store}).Run(ctx); err != nil {
			slog.Error("printer observation stopped", "error", err)
			stop()
		}
	}()
	archive := snapshots.Store{Directory: configuration.snapshotDirectory}
	if err := archive.Cleanup(ctx, configuration.snapshotRetention); err != nil {
		return err
	}
	client := &http.Client{Timeout: server.SnapshotTimeout}
	media := server.Media{
		Camera:    camera.Client{URL: configuration.cameraURL, HTTPClient: client},
		Archive:   archive,
		History:   history.Client{URL: configuration.historyURL, HTTPClient: client},
		Status:    store,
		Retention: configuration.snapshotRetention,
	}
	controller := control.New(store, printer.Commander{URL: configuration.printerURL})
	service := &http.Server{
		Handler: server.New(store,
			server.WithTelemetryFreshness(configuration.telemetryFreshness),
			server.WithTools(media.Tools()...),
			server.WithTools(server.Controls{Controller: controller}.Tools()...),
		),
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
