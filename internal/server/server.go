// Package server exposes carbonctl's bounded loopback transport.
package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
)

const (
	HeaderTimeout             = 5 * time.Second
	ShutdownTimeout           = 5 * time.Second
	ReadTimeout               = 10 * time.Second
	WriteTimeout              = 30 * time.Second
	IdleTimeout               = 60 * time.Second
	DefaultTelemetryFreshness = 90 * time.Second
)

var ErrNonLoopback = errors.New("carbonctl listener must use a loopback address")

type StatusSource interface {
	Current() printer.Status
}

type options struct {
	now       func() time.Time
	freshness time.Duration
	tools     []Tool
}

type Option func(*options)

func WithTelemetryFreshness(freshness time.Duration) Option {
	return func(configuration *options) {
		if freshness > 0 {
			configuration.freshness = freshness
		}
	}
}

func New(source StatusSource, supplied ...Option) http.Handler {
	configuration := options{now: time.Now, freshness: DefaultTelemetryFreshness}
	for _, option := range supplied {
		option(&configuration)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", jsonHandler(map[string]any{"status": "ok"}))
	mux.HandleFunc("GET /ready", func(writer http.ResponseWriter, _ *http.Request) {
		current := source.Current()
		connected := current.Connected
		fresh := !current.ObservedAt.IsZero() &&
			configuration.now().Sub(current.ObservedAt) <= configuration.freshness
		status := "unavailable"
		if connected && fresh {
			status = "ready"
		}
		writeJSON(writer, map[string]any{"status": status, "printer_connected": connected})
	})
	mux.HandleFunc("GET /identity", jsonHandler(map[string]any{
		"name": "carbonctl-mcp", "version": "1",
	}))
	mux.HandleFunc("POST /mcp", func(writer http.ResponseWriter, request *http.Request) {
		var incoming struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10))
		if decoder.Decode(&incoming) != nil || requireEOF(decoder) != nil || incoming.JSONRPC != "2.0" {
			writeJSON(writer, map[string]any{"jsonrpc": "2.0", "error": map[string]any{
				"code": -32700, "message": "invalid JSON-RPC request",
			}})
			return
		}
		response := map[string]any{"jsonrpc": "2.0", "id": incoming.ID}
		switch incoming.Method {
		case "initialize":
			response["result"] = map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]string{"name": "carbonctl", "version": "1"},
			}
		case "tools/list":
			tools := []any{map[string]any{
				"name":        "carbonctl_get_status",
				"description": "Observe the current printer state",
				"inputSchema": map[string]any{"type": "object", "additionalProperties": false},
			}}
			for _, tool := range configuration.tools {
				tools = append(tools, map[string]any{
					"name": tool.Name, "description": tool.Description, "inputSchema": tool.InputSchema,
				})
			}
			response["result"] = map[string]any{"tools": tools}
		case "tools/call":
			if incoming.Params.Name == "carbonctl_get_status" {
				current := source.Current()
				response["result"] = map[string]any{
					"content":           []any{map[string]any{"type": "text", "text": mustJSON(current)}},
					"structuredContent": current, "isError": false,
				}
				break
			}
			tool, ok := configuration.tool(incoming.Params.Name)
			if !ok {
				response["error"] = map[string]any{"code": -32602, "message": "unknown tool"}
				break
			}
			result, err := tool.Call(request.Context(), incoming.Params.Arguments)
			if err != nil {
				response["error"] = map[string]any{"code": -32603, "message": "tool failed"}
				break
			}
			response["result"] = result
		default:
			response["error"] = map[string]any{"code": -32601, "message": "method not found"}
		}
		writeJSON(writer, response)
	})
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
