// Package server exposes carbonctl's bounded loopback transport.
package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/printer"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	HeaderTimeout             = 5 * time.Second
	ShutdownTimeout           = 5 * time.Second
	ReadTimeout               = 10 * time.Second
	WriteTimeout              = 6 * time.Minute
	IdleTimeout               = 60 * time.Second
	DefaultTelemetryFreshness = 90 * time.Second
)

var ErrNonLoopback = errors.New("carbonctl listener must use a loopback address")

type StatusSource interface {
	Current() printer.Status
}

type options struct {
	tools []Tool
}

type Option func(*options)

func New(source StatusSource, supplied ...Option) http.Handler {
	configuration := options{}
	for _, option := range supplied {
		option(&configuration)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", jsonHandler(map[string]any{"status": "ok"}))
	mux.HandleFunc("GET /ready", func(writer http.ResponseWriter, _ *http.Request) {
		current := source.Current()
		connected := current.Connected
		fresh := !current.ObservedAt.IsZero() &&
			time.Since(current.ObservedAt) <= DefaultTelemetryFreshness
		status := "unavailable"
		if connected && fresh {
			status = "ready"
		}
		writeJSON(writer, map[string]any{"status": status, "printer_connected": connected})
	})
	mux.HandleFunc("GET /identity", jsonHandler(map[string]any{
		"name": "carbonctl-mcp", "version": "1",
	}))
	mcpServer := NewMCPServer(source, configuration.tools...)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return mcpServer
	}, nil))
	return mux
}

func requireEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON request must contain exactly one value")
	}
	return nil
}

func jsonHandler(value any) http.HandlerFunc {
	return func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, value)
	}
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}

func mustJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
