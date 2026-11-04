package server_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/carbonctl/internal/control"
	"git.kuhree.com/kuhree.com/carbonctl/internal/printer"
	"git.kuhree.com/kuhree.com/carbonctl/internal/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStopIsOneCallThatSendsSDCP130(t *testing.T) {
	source := &mutableStatus{status: printer.Status{
		Connected: true, State: "printing", TaskID: "task-42",
		Filename: "benchy.gcode",
	}}
	sender := &observingSender{source: source}
	controller := control.New(source, sender,
		control.WithConfirmationTimeout(100*time.Millisecond),
		control.WithPollInterval(time.Millisecond),
	)
	session := connectMCP(t, source, controller, nil)

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "carbonctl_stop", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("stop = %#v", result)
	}
	if got := sender.sent(); len(got) != 1 || got[0] != 130 {
		t.Fatalf("commands = %v, want [130]", got)
	}
}

func TestStopWithoutAnActivePrintIsAToolErrorAndSendsNothing(t *testing.T) {
	source := &mutableStatus{status: printer.Status{Connected: true, State: "idle"}}
	sender := &observingSender{source: source}
	controller := control.New(source, sender)
	session := connectMCP(t, source, controller, nil)

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "carbonctl_stop", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("stop = %#v, want tool error", result)
	}
	if got := sender.sent(); len(got) != 0 {
		t.Fatalf("commands = %v, want none", got)
	}
}

func connectMCP(
	t *testing.T,
	source *mutableStatus,
	controller *control.Manager,
	options *mcp.ClientOptions,
) *mcp.ClientSession {
	t.Helper()
	mcpServer := server.NewMCPServer(source, server.Controls{Controller: controller}.Tools()...)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := mcpServer.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "carbonctl-test", Version: "1"}, options)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
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
