package carbonctl_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestProcessControlsFakePrinterThroughMCP(t *testing.T) {
	fake := newControlPrinter(t)
	address := freeAddress(t)
	process := startProcess(t, address, fake.url, t.TempDir()+"/state.json")
	waitForHTTP(t, "http://"+address+"/health", process)
	waitForConnected(t, address)

	paused := processTool(t, address, "carbonctl_pause", `{}`)
	if outcome(paused) != "confirmed" {
		t.Fatalf("pause response = %#v", paused)
	}
	if command := <-fake.commands; command != 129 {
		t.Fatalf("pause command = %d, want 129", command)
	}

	prepared := processTool(t, address, "carbonctl_prepare_stop", `{}`)
	token := prepared["result"].(map[string]any)["structuredContent"].(map[string]any)["token"].(string)
	stopped := processTool(t, address, "carbonctl_confirm_stop", `{"token":"`+token+`"}`)
	if outcome(stopped) != "confirmed" {
		t.Fatalf("stop response = %#v", stopped)
	}
	if command := <-fake.commands; command != 130 {
		t.Fatalf("stop command = %d, want 130", command)
	}

	reused := processTool(t, address, "carbonctl_confirm_stop", `{"token":"`+token+`"}`)
	if reused["result"].(map[string]any)["isError"] != true {
		t.Fatalf("reused confirmation = %#v, want tool error", reused)
	}
	select {
	case command := <-fake.commands:
		t.Fatalf("unexpected replayed command %d", command)
	case <-time.After(50 * time.Millisecond):
	}
}

func waitForConnected(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if callStatus(t, "http://"+address+"/mcp")["connected"] == true {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("fake printer did not connect")
}

func processTool(t *testing.T, address, name, arguments string) map[string]any {
	t.Helper()
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` +
		name + `","arguments":` + arguments + `}}`
	response, err := http.Post("http://"+address+"/mcp", "application/json", bytes.NewBufferString(request))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func outcome(response map[string]any) string {
	return response["result"].(map[string]any)["structuredContent"].(map[string]any)["outcome"].(string)
}

type controlPrinter struct {
	url      string
	commands chan int

	mu          sync.Mutex
	state       string
	connections map[*lockedWebSocket]struct{}
}

type lockedWebSocket struct {
	mu         sync.Mutex
	connection *websocket.Conn
}

func newControlPrinter(t *testing.T) *controlPrinter {
	t.Helper()
	fake := &controlPrinter{
		commands: make(chan int, 4), state: "printing",
		connections: make(map[*lockedWebSocket]struct{}),
	}
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		locked := &lockedWebSocket{connection: connection}
		fake.mu.Lock()
		fake.connections[locked] = struct{}{}
		fake.mu.Unlock()
		defer func() {
			fake.mu.Lock()
			delete(fake.connections, locked)
			fake.mu.Unlock()
			_ = connection.Close()
		}()
		fake.broadcast()
		for {
			_, payload, err := connection.ReadMessage()
			if err != nil {
				return
			}
			var command struct {
				Data struct {
					Command   int    `json:"Cmd"`
					RequestID string `json:"RequestID"`
				} `json:"Data"`
			}
			if json.Unmarshal(payload, &command) != nil || command.Data.RequestID == "" {
				continue
			}
			fake.commands <- command.Data.Command
			locked.write(map[string]any{
				"Topic": "sdcp/response/board-7",
				"Data": map[string]any{
					"Cmd": command.Data.Command, "RequestID": command.Data.RequestID,
					"MainboardID": "board-7", "Data": map[string]any{"Ack": 0},
				},
			})
			fake.mu.Lock()
			switch command.Data.Command {
			case 129:
				fake.state = "paused"
			case 131:
				fake.state = "printing"
			case 130:
				fake.state = "idle"
			}
			fake.mu.Unlock()
			fake.broadcast()
		}
	}))
	t.Cleanup(server.Close)
	fake.url = "ws" + strings.TrimPrefix(server.URL, "http")
	return fake
}

func (fake *controlPrinter) broadcast() {
	fake.mu.Lock()
	state := fake.state
	connections := make([]*lockedWebSocket, 0, len(fake.connections))
	for connection := range fake.connections {
		connections = append(connections, connection)
	}
	fake.mu.Unlock()
	machineState := 1
	printState := 1
	taskID := "task-42"
	if state == "paused" {
		printState = 6
	}
	if state == "idle" {
		machineState = 0
		printState = 8
		taskID = ""
	}
	message := map[string]any{
		"MainboardID": "board-7",
		"Status": map[string]any{
			"CurrentStatus": []int{machineState},
			"PrintInfo": map[string]any{
				"Status": printState, "TaskId": taskID, "Filename": "benchy.gcode",
			},
		},
	}
	for _, connection := range connections {
		connection.write(message)
	}
}

func (connection *lockedWebSocket) write(value any) {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	_ = connection.connection.WriteJSON(value)
}
