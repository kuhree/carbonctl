package main

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadTelegramChatIDAcceptsOneCanonicalPositiveNumericID(t *testing.T) {
	path := writeCredential(t, "123456789\n")
	got, err := readTelegramChatID(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "123456789" {
		t.Fatalf("chat ID = %q, want canonical value", got)
	}
}

func TestReadTelegramChatIDAcceptsEnvFilePrefixedValue(t *testing.T) {
	path := writeCredential(t, "TELEGRAM_ALLOWED_USERS=123456789\n")
	got, err := readTelegramChatID(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "123456789" {
		t.Fatalf("chat ID = %q, want canonical value", got)
	}
}

func TestReadCredentialAcceptsEnvFilePrefixedValue(t *testing.T) {
	path := writeCredential(t, "TELEGRAM_BOT_TOKEN=abc:123\n")
	got, err := readCredential(path, "TELEGRAM_BOT_TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if got != "abc:123" {
		t.Fatalf("token = %q, want stripped value", got)
	}
}

func TestReadTelegramChatIDRejectsNonCanonicalOrMultipleValues(t *testing.T) {
	tests := map[string]string{
		"empty":             "",
		"zero":              "0",
		"leading zero":      "0123",
		"positive sign":     "+123",
		"negative sign":     "-123",
		"surrounding space": " 123 ",
		"internal space":    "12 3",
		"username":          "@owner",
		"comma list":        "123,456",
		"multiple lines":    "123\n456",
		"int64 overflow":    "9223372036854775808",
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := readTelegramChatID(writeCredential(t, payload))
			if !errors.Is(err, errInvalidTelegramChatID) {
				t.Fatalf("error = %v, want invalid Telegram chat ID", err)
			}
		})
	}
}

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

func writeCredential(t *testing.T, payload string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
