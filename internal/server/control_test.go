package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/control"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/server"
)

func TestControlToolsExposeOnlyTheApprovedMutationBoundary(t *testing.T) {
	source := &mutableStatus{status: printer.Status{
		Connected: true, State: "printing", TaskID: "task-42",
	}}
	controller := control.New(source, &observingSender{source: source},
		control.WithConfirmationTimeout(100*time.Millisecond),
		control.WithPollInterval(time.Millisecond),
	)
	handler := server.New(source, server.WithTools(server.Controls{Controller: controller}.Tools()...))

	payload := callRPC(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	result := payload["result"].(map[string]any)
	tools := result["tools"].([]any)
	names := make([]string, 0, len(tools))
	for _, raw := range tools {
		names = append(names, raw.(map[string]any)["name"].(string))
	}
	want := []string{
		"carbonctl_get_status",
		"carbonctl_pause",
		"carbonctl_resume",
		"carbonctl_prepare_stop",
		"carbonctl_confirm_stop",
	}
	if len(names) != len(want) {
		t.Fatalf("tool names = %v, want %v", names, want)
	}
	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("tool names = %v, want %v", names, want)
		}
	}
}

func TestControlToolsPauseAndConfirmStopThroughMCP(t *testing.T) {
	source := &mutableStatus{status: printer.Status{
		Connected: true, State: "printing", TaskID: "task-42",
		Filename: "benchy.gcode",
	}}
	sender := &observingSender{source: source}
	controller := control.New(source, sender,
		control.WithConfirmationTimeout(100*time.Millisecond),
		control.WithPollInterval(time.Millisecond),
	)
	handler := server.New(source, server.WithTools(server.Controls{Controller: controller}.Tools()...))

	paused := callTool(t, handler, "carbonctl_pause", `{}`)
	assertOutcome(t, paused, "confirmed")

	prepared := callTool(t, handler, "carbonctl_prepare_stop", `{}`)
	preparedResult := prepared["result"].(map[string]any)
	structured := preparedResult["structuredContent"].(map[string]any)
	token, ok := structured["token"].(string)
	if !ok || token == "" || structured["task_id"] != "task-42" {
		t.Fatalf("prepared stop = %#v", structured)
	}

	stopped := callTool(t, handler, "carbonctl_confirm_stop",
		`{"token":"`+token+`"}`)
	assertOutcome(t, stopped, "confirmed")
	if got := sender.sent(); len(got) != 2 || got[0] != 129 || got[1] != 130 {
		t.Fatalf("commands = %v, want [129 130]", got)
	}
}

func TestConfirmStopRejectsUnknownArgumentsWithoutSending(t *testing.T) {
	source := &mutableStatus{status: printer.Status{
		Connected: true, State: "printing", TaskID: "task-42",
	}}
	sender := &observingSender{source: source}
	controller := control.New(source, sender)
	handler := server.New(source, server.WithTools(server.Controls{Controller: controller}.Tools()...))

	response := callTool(t, handler, "carbonctl_confirm_stop", `{"token":"x","extra":true}`)

	result := response["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("result = %#v, want MCP tool error", result)
	}
	if len(sender.sent()) != 0 {
		t.Fatalf("commands = %v, want none", sender.sent())
	}
}

func callTool(t *testing.T, handler http.Handler, name, arguments string) map[string]any {
	t.Helper()
	return callRPC(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+
		name+`","arguments":`+arguments+`}}`)
}

func callRPC(t *testing.T, handler http.Handler, request string) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(request)))
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func assertOutcome(t *testing.T, response map[string]any, want string) {
	t.Helper()
	result := response["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatalf("result = %#v, want success", result)
	}
	structured := result["structuredContent"].(map[string]any)
	if structured["outcome"] != want {
		t.Fatalf("outcome = %#v, want %q", structured["outcome"], want)
	}
}

type mutableStatus struct {
	mu     sync.Mutex
	status printer.Status
}

func (source *mutableStatus) Current() printer.Status {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.status
}

func (source *mutableStatus) set(status printer.Status) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.status = status
}

type observingSender struct {
	mu       sync.Mutex
	source   *mutableStatus
	commands []int
}

func (sender *observingSender) Send(_ context.Context, command int, current printer.Status) error {
	sender.mu.Lock()
	sender.commands = append(sender.commands, command)
	sender.mu.Unlock()
	switch command {
	case 129:
		current.State = "paused"
	case 131:
		current.State = "printing"
	case 130:
		current.State = "idle"
		current.TaskID = ""
	}
	sender.source.set(current)
	return nil
}

func (sender *observingSender) sent() []int {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return append([]int(nil), sender.commands...)
}
