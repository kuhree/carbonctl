// Package server exposes carbonctl's bounded loopback transport.
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
)

const (
	HeaderTimeout   = 5 * time.Second
	ShutdownTimeout = 5 * time.Second
)

var ErrNonLoopback = errors.New("carbonctl listener must use a loopback address")

type StatusSource interface {
	Current() printer.Status
}

func New(source StatusSource) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", jsonHandler(map[string]any{"status": "ok"}))
	mux.HandleFunc("GET /ready", func(writer http.ResponseWriter, _ *http.Request) {
		connected := source.Current().Connected
		status := "unavailable"
		if connected {
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
				Name string `json:"name"`
			} `json:"params"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10))
		if decoder.Decode(&incoming) != nil || incoming.JSONRPC != "2.0" {
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
			response["result"] = map[string]any{"tools": []any{map[string]any{
				"name":        "carbonctl_get_status",
				"description": "Observe the current printer state",
				"inputSchema": map[string]any{"type": "object", "additionalProperties": false},
			}}}
		case "tools/call":
			if incoming.Params.Name != "carbonctl_get_status" {
				response["error"] = map[string]any{"code": -32602, "message": "unknown tool"}
				break
			}
			current := source.Current()
			response["result"] = map[string]any{
				"content":           []any{map[string]any{"type": "text", "text": mustJSON(current)}},
				"structuredContent": current, "isError": false,
			}
		default:
			response["error"] = map[string]any{"code": -32601, "message": "method not found"}
		}
		writeJSON(writer, response)
	})
	return mux
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
