package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/server"
)

func TestMCPRejectsTrailingJSONValues(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize"} {"second":true}`,
	))
	response := httptest.NewRecorder()
	server.New(staticStatus{}).ServeHTTP(response, request)

	if !strings.Contains(response.Body.String(), `"code":-32700`) {
		t.Fatalf("response = %s, want JSON-RPC parse error", response.Body.String())
	}
}

func TestReadyRequiresFreshTelemetry(t *testing.T) {
	source := staticStatus{status: printer.Status{
		Connected:  true,
		ObservedAt: time.Now().Add(-10 * time.Minute),
	}}
	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	response := httptest.NewRecorder()
	server.New(source).ServeHTTP(response, request)

	if got := response.Body.String(); got != `{"printer_connected":true,"status":"unavailable"}`+"\n" {
		t.Fatalf("ready response = %s", got)
	}
}

type staticStatus struct {
	status printer.Status
}

func (source staticStatus) Current() printer.Status {
	return source.status
}
