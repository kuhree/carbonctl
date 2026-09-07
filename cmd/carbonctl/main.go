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
	"strconv"
	"strings"
	"sync"
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
	listenAddress     string
	printerURL        string
	statePath         string
	cameraURL         string
	historyURL        string
	snapshotDirectory string
	snapshotRetention time.Duration
	telegramTokenFile string
	telegramChatFile  string
}

var errInvalidTelegramChatID = errors.New("invalid Telegram chat ID")

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
	configuration.telegramTokenFile = os.Getenv("CARBONCTL_TELEGRAM_TOKEN_FILE")
	configuration.telegramChatFile = os.Getenv("CARBONCTL_TELEGRAM_CHAT_FILE")

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
	observer := printer.Observer(store)
	if configuration.telegramTokenFile != "" || configuration.telegramChatFile != "" {
		if configuration.telegramTokenFile == "" || configuration.telegramChatFile == "" {
			return errors.New("both Telegram credential files are required")
		}
		token, err := readCredential(configuration.telegramTokenFile, "TELEGRAM_BOT_TOKEN")
		if err != nil {
			return errors.New("read Telegram token")
		}
		chatID, err := readTelegramChatID(configuration.telegramChatFile)
		if err != nil {
			return errors.New("read Telegram chat identity")
		}
		engine := lifecycle.New(store, lifecycle.RetainedSnapshots{
			Camera:  camera.Client{URL: configuration.cameraURL, HTTPClient: client},
			Archive: archive, Retention: configuration.snapshotRetention,
			Timeout: server.SnapshotTimeout,
		}, notifications.Telegram{
			Token: token, ChatID: chatID,
			HTTPClient: client,
		})
		observer = lifecycle.Observer{Context: ctx, Engine: engine}
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := engine.Run(ctx); err != nil {
				slog.Error("printer lifecycle monitoring stopped", "error", err)
				stop()
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
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

func readTelegramChatID(path string) (string, error) {
	value, err := readCredential(path, "TELEGRAM_ALLOWED_USERS")
	if err != nil || value[0] == '0' {
		return "", errInvalidTelegramChatID
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return "", errInvalidTelegramChatID
		}
	}
	if _, err := strconv.ParseUint(value, 10, 63); err != nil {
		return "", errInvalidTelegramChatID
	}
	return value, nil
}

// readCredential reads a credential file, tolerating an optional "KEY="
// prefix so the same file can double as a systemd EnvironmentFile= entry.
func readCredential(path, key string) (string, error) {
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
	if rest, ok := strings.CutPrefix(value, key+"="); ok {
		value = rest
	}
	if value == "" {
		return "", errors.New("credential is empty")
	}
	return value, nil
}
