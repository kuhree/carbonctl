package printer_test

import (
	"context"
	"net"
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

func TestStatusNormalizationExposesPausedTaskAndBoardIdentityForControls(t *testing.T) {
	status, err := printer.ParseStatus([]byte(`{
		"MainboardID":"board-7",
		"Status":{"CurrentStatus":[1],"PrintInfo":{
			"Status":6,"TaskId":"task-42","Filename":"benchy.gcode"
		}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "paused" || status.TaskID != "task-42" ||
		status.MainboardID != "board-7" {
		t.Fatalf("normalized paused status = %#v", status)
	}
}

func TestClientBoundsEachWebSocketDialAttempt(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			defer connection.Close()
			<-time.After(2 * time.Second)
		}
	}()

	observer := &disconnectObserver{disconnected: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = (printer.Client{
			URL: "ws://" + listener.Addr().String(), Observer: observer,
		}).Run(ctx)
	}()

	select {
	case <-observer.disconnected:
	case <-time.After(time.Second):
		t.Fatal("WebSocket dial attempt exceeded its bounded timeout")
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

func (observer *recordingObserver) Observe(status printer.Status) error {
	observer.observations <- status
	return nil
}

func (observer *recordingObserver) Disconnected() error { return nil }

type disconnectObserver struct {
	disconnected chan struct{}
}

func (observer *disconnectObserver) Observe(printer.Status) error { return nil }

func (observer *disconnectObserver) Disconnected() error {
	select {
	case observer.disconnected <- struct{}{}:
	default:
	}
	return nil
}
