package printer_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/carbonctl/internal/printer"
	"github.com/gorilla/websocket"
)

func TestCommanderSendsOneDocumentedSDCPRequest(t *testing.T) {
	var received map[string]any
	url := fakeCommandPrinter(t, func(payload []byte) int {
		if err := json.Unmarshal(payload, &received); err != nil {
			t.Fatal(err)
		}
		return 0
	})
	commander := printer.Commander{URL: url}

	err := commander.Send(context.Background(), 129, printer.Status{
		MainboardID: "board-7", TaskID: "task-42",
	})

	if err != nil {
		t.Fatal(err)
	}
	data, ok := received["Data"].(map[string]any)
	if !ok {
		t.Fatalf("request Data = %#v", received["Data"])
	}
	if received["Topic"] != "sdcp/request/board-7" ||
		data["Cmd"] != float64(129) ||
		data["MainboardID"] != "board-7" ||
		data["From"] != float64(0) ||
		data["RequestID"] == "" ||
		received["Id"] == "" {
		t.Fatalf("request = %#v", received)
	}
	if body, ok := data["Data"].(map[string]any); !ok || len(body) != 0 {
		t.Fatalf("command data = %#v, want empty object", data["Data"])
	}
}

func TestCommanderDoesNotReplayRejectedOrDroppedCommands(t *testing.T) {
	tests := []struct {
		name string
		ack  int
		drop bool
	}{
		{name: "rejected response", ack: 1},
		{name: "dropped connection", drop: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			url := fakeCommandPrinter(t, func([]byte) int {
				requests.Add(1)
				if test.drop {
					return -1
				}
				return test.ack
			})
			commander := printer.Commander{URL: url}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			err := commander.Send(ctx, 130, printer.Status{
				MainboardID: "board-7", TaskID: "task-42",
			})

			if err == nil {
				t.Fatal("Send() error = nil, want command failure")
			}
			if requests.Load() != 1 {
				t.Fatalf("requests = %d, want exactly one", requests.Load())
			}
		})
	}
}

func fakeCommandPrinter(t *testing.T, response func([]byte) int) string {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_, payload, err := connection.ReadMessage()
		if err != nil {
			return
		}
		ack := response(payload)
		if ack < 0 {
			return
		}
		var incoming struct {
			Data struct {
				Cmd       int    `json:"Cmd"`
				RequestID string `json:"RequestID"`
			} `json:"Data"`
		}
		if err := json.Unmarshal(payload, &incoming); err != nil {
			return
		}
		_ = connection.WriteJSON(map[string]any{
			"Topic": "sdcp/response/board-7",
			"Data": map[string]any{
				"Cmd":         incoming.Data.Cmd,
				"Data":        map[string]any{"Ack": ack},
				"RequestID":   incoming.Data.RequestID,
				"MainboardID": "board-7",
			},
		})
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}
