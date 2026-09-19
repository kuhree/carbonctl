package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/camera"
	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/control"
	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/history"
	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/printer"
	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/server"
	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/snapshots"
	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/state"
)

type config struct {
	listenAddress     string
	printerURL        string
	statePath         string
	cameraURL         string
	historyURL        string
	snapshotDirectory string
	snapshotRetention time.Duration
}

func main() {
	var configuration config
	flag.StringVar(&configuration.listenAddress, "listen", "127.0.0.1:9086", "loopback HTTP listen address")
	flag.StringVar(&configuration.printerURL, "printer-url", "", "OpenCentauri WebSocket URL")
	flag.StringVar(&configuration.statePath, "state", "", "persistent state document path")
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
	var workers sync.WaitGroup
	defer func() {
		stop()
		workers.Wait()
	}()
	store, err := state.Open(configuration.statePath)
	if err != nil {
		return err
	}
	archive := snapshots.Store{Directory: configuration.snapshotDirectory}
	if err := archive.Cleanup(ctx, configuration.snapshotRetention); err != nil {
		return err
	}
	workers.Add(1)
	cleanupInterval := snapshots.CleanupInterval(configuration.snapshotRetention, 0)
	go func() {
		defer workers.Done()
		snapshots.RunCleanup(ctx, archive, configuration.snapshotRetention, cleanupInterval, func(err error) {
			slog.Warn("snapshot retention cleanup failed", "error", err)
		})
	}()
	client := &http.Client{Timeout: server.SnapshotTimeout}
	workers.Add(1)
	go func() {
		defer workers.Done()
		if err := (printer.Client{URL: configuration.printerURL, Observer: printer.Observer(store)}).Run(ctx); err != nil {
			slog.Error("printer observation stopped", "error", err)
			stop()
		}
	}()
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
			server.WithTools(media.Tools()...),
			server.WithTools(server.Controls{Controller: controller}.Tools()...),
		),
		ReadHeaderTimeout: server.HeaderTimeout,
		ReadTimeout:       server.ReadTimeout,
		WriteTimeout:      server.WriteTimeout,
		IdleTimeout:       server.IdleTimeout,
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), server.ShutdownTimeout)
		defer cancel()
		_ = service.Shutdown(shutdownCtx)
	}()
	serveErr := service.Serve(listener)
	stop()
	<-shutdownDone
	workers.Wait()
	if errors.Is(serveErr, http.ErrServerClosed) {
		return nil
	}
	return serveErr
}
