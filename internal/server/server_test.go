package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/printer"
	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/server"
)

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
