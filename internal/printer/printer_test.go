package printer_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
	"github.com/gorilla/websocket"
)

func TestStatusNormalizationAcceptsDocumentedAlternateNesting(t *testing.T) {
	status, err := printer.ParseStatus([]byte(`{
		"CurrentStatus":[99],
		"PrintInfo":{"TaskID":"alternate-task","FileName":"alternate.gcode",
			"CurrentLayer":4,"TotalLayers":9},
		"Status":{"TempOfNozzle":201,"TempOfHotbed":58}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "unknown" || status.TaskID != "alternate-task" ||
		status.Filename != "alternate.gcode" || status.CurrentLayer != 4 ||
		status.TotalLayers != 9 || status.Temperatures.Nozzle != 201 ||
		status.Temperatures.Bed != 58 {
		t.Fatalf("normalized status = %#v", status)
	}
}

func TestClientReconnectsAfterAnUnsafePayload(t *testing.T) {
	upgrader := websocket.Upgrader{}
	attempts := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		attempts <- struct{}{}
		if len(attempts) == 1 {
			_ = connection.WriteMessage(websocket.TextMessage, make([]byte, (1<<20)+1))
			return
		}
		_ = connection.WriteMessage(websocket.TextMessage, []byte(
			`{"Status":{"CurrentStatus":[1],"PrintInfo":{"TaskId":"reconnected"}}}`,
		))
		<-request.Context().Done()
	}))
	defer server.Close()

	observer := &recordingObserver{observations: make(chan printer.Status, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go (printer.Client{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"), Observer: observer,
	}).Run(ctx)

	select {
	case status := <-observer.observations:
		if status.TaskID != "reconnected" || !status.Connected {
			t.Fatalf("status after reconnect = %#v", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client did not reconnect after rejecting oversized payload")
	}
}

type recordingObserver struct {
	observations chan printer.Status
}

func (observer *recordingObserver) Observe(status printer.Status) {
	observer.observations <- status
}

func (observer *recordingObserver) Disconnected() {}
