package main

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRunTreatsSignalShutdownAsSuccess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(config{
			listenAddress: address,
			printerURL:    "ws://127.0.0.1:1/websocket",
			statePath:     filepath.Join(t.TempDir(), "state.json"),
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, requestErr := http.Get("http://" + address + "/health")
		if requestErr == nil {
			_ = response.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if time.Now().After(deadline) {
		t.Fatal("carbonctl did not start")
	}
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run() error = %v, want clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not stop after SIGTERM")
	}
}
