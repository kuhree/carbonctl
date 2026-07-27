package main

import (
	"context"
	"errors"
	"flag"
	"io"
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
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/lifecycle"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/notifications"
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
	telegramAPIURL     string
	telegramTokenFile  string
	telegramChatFile   string
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
	flag.StringVar(&configuration.telegramAPIURL, "telegram-api-url", "", "Telegram Bot API base URL")
	flag.StringVar(&configuration.telegramTokenFile, "telegram-token-file", "", "Telegram bot token file")
	flag.StringVar(&configuration.telegramChatFile, "telegram-chat-file", "", "authorized Telegram chat ID file")
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
	archive := snapshots.Store{Directory: configuration.snapshotDirectory}
	if err := archive.Cleanup(ctx, configuration.snapshotRetention); err != nil {
		return err
	}
	client := &http.Client{Timeout: server.SnapshotTimeout}
	observer := printer.Observer(store)
	if configuration.telegramTokenFile != "" || configuration.telegramChatFile != "" {
		if configuration.telegramTokenFile == "" || configuration.telegramChatFile == "" {
			return errors.New("both Telegram credential files are required")
		}
		token, err := readCredential(configuration.telegramTokenFile)
		if err != nil {
			return errors.New("read Telegram token")
		}
		chatID, err := readCredential(configuration.telegramChatFile)
		if err != nil {
			return errors.New("read Telegram chat identity")
		}
		engine := lifecycle.New(store, lifecycle.RetainedSnapshots{
			Camera:  camera.Client{URL: configuration.cameraURL, HTTPClient: client},
			Archive: archive, Retention: configuration.snapshotRetention,
			Timeout: server.SnapshotTimeout,
		}, notifications.Telegram{
			BaseURL: configuration.telegramAPIURL, Token: token, ChatID: chatID,
			HTTPClient: client,
		})
		observer = lifecycle.Observer{Context: ctx, Engine: engine}
		go func() {
			if err := engine.Run(ctx); err != nil {
				slog.Error("printer lifecycle monitoring stopped", "error", err)
				stop()
			}
		}()
	}
	go func() {
		if err := (printer.Client{URL: configuration.printerURL, Observer: observer}).Run(ctx); err != nil {
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
			server.WithTelemetryFreshness(configuration.telemetryFreshness),
			server.WithOfficialMCP(),
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

func readCredential(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, 1025))
	if err != nil || len(payload) > 1024 {
		return "", errors.New("credential is unreadable or too large")
	}
	value := string(payload)
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r') {
		value = value[:len(value)-1]
	}
	if value == "" {
		return "", errors.New("credential is empty")
	}
	return value, nil
}
