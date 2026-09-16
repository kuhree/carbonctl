package server_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/platform/bin/carbonctl/internal/control"
	"git.kuhree.com/kuhree.com/platform/bin/carbonctl/internal/printer"
	"git.kuhree.com/kuhree.com/platform/bin/carbonctl/internal/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConfirmStopRequiresAcceptedMCPFormElicitation(t *testing.T) {
	tests := []struct {
		name       string
		client     *mcp.ClientOptions
		wantMutate bool
	}{
		{
			name: "accept",
			client: &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
				if !strings.Contains(request.Params.Message, "benchy.gcode") {
					return nil, fmt.Errorf("elicitation message = %q, want active filename", request.Params.Message)
				}
				schema, ok := request.Params.RequestedSchema.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("elicitation schema = %#v, want object schema", request.Params.RequestedSchema)
				}
				if properties, present := schema["properties"]; present {
					return nil, fmt.Errorf("elicitation properties = %#v, want no requested fields", properties)
				}
				if schema["type"] != "object" || schema["additionalProperties"] != false {
					return nil, fmt.Errorf("elicitation schema = %#v, want closed empty object", schema)
				}
				return &mcp.ElicitResult{Action: "accept", Content: map[string]any{}}, nil
			}},
			wantMutate: true,
		},
		{
			name: "decline",
			client: &mcp.ClientOptions{ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
				return &mcp.ElicitResult{Action: "decline"}, nil
			}},
		},
		{
			name: "cancel",
			client: &mcp.ClientOptions{ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
				return &mcp.ElicitResult{Action: "cancel"}, nil
			}},
		},
		{name: "client has no elicitation capability", client: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &mutableStatus{status: printer.Status{
				Connected: true, State: "printing", TaskID: "task-42",
				Filename: "benchy.gcode",
			}}
			sender := &observingSender{source: source}
			controller := control.New(source, sender,
				control.WithConfirmationTimeout(100*time.Millisecond),
				control.WithPollInterval(time.Millisecond),
			)
			session := connectMCP(t, source, controller, test.client)
			prepared, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "carbonctl_prepare_stop", Arguments: map[string]any{},
			})
			if err != nil {
				t.Fatal(err)
			}
			token := prepared.StructuredContent.(map[string]any)["token"].(string)

			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "carbonctl_confirm_stop", Arguments: map[string]any{"token": token},
			})
			if err != nil {
				t.Fatal(err)
			}
			if test.wantMutate {
				if result.IsError {
					t.Fatalf("accepted stop = %#v", result)
				}
				if got := sender.sent(); len(got) != 1 || got[0] != 130 {
					t.Fatalf("commands = %v, want [130]", got)
				}
			} else {
				if !result.IsError {
					t.Fatalf("unapproved stop = %#v, want tool error", result)
				}
				if got := sender.sent(); len(got) != 0 {
					t.Fatalf("commands = %v, want none", got)
				}
			}
		})
	}
}

func TestConfirmStopCannotMutateBeforeElicitationAccepts(t *testing.T) {
	source := &mutableStatus{status: printer.Status{
		Connected: true, State: "printing", TaskID: "task-42",
		Filename: "benchy.gcode",
	}}
	sender := &observingSender{source: source}
	controller := control.New(source, sender,
		control.WithConfirmationTimeout(100*time.Millisecond),
		control.WithPollInterval(time.Millisecond),
	)
	requested := make(chan struct{})
	accept := make(chan struct{})
	session := connectMCP(t, source, controller, &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			close(requested)
			<-accept
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{}}, nil
		},
	})
	prepared, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "carbonctl_prepare_stop", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	token := prepared.StructuredContent.(map[string]any)["token"].(string)
	finished := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, _ := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "carbonctl_confirm_stop", Arguments: map[string]any{"token": token},
		})
		finished <- result
	}()

	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("client did not receive elicitation")
	}
	if got := sender.sent(); len(got) != 0 {
		t.Fatalf("commands before approval = %v, want none", got)
	}
	close(accept)
	select {
	case result := <-finished:
		if result == nil || result.IsError {
			t.Fatalf("accepted stop result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmed stop did not complete")
	}
	if got := sender.sent(); len(got) != 1 || got[0] != 130 {
		t.Fatalf("commands after approval = %v, want [130]", got)
	}
}

func TestConfirmStopElicitationTimeoutDoesNotMutate(t *testing.T) {
	source := &mutableStatus{status: printer.Status{
		Connected: true, State: "printing", TaskID: "task-42",
		Filename: "benchy.gcode",
	}}
	sender := &observingSender{source: source}
	controller := control.New(source, sender)
	session := connectMCP(t, source, controller, &mcp.ClientOptions{
		ElicitationHandler: func(ctx context.Context, _ *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	prepared, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "carbonctl_prepare_stop", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	token := prepared.StructuredContent.(map[string]any)["token"].(string)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, _ = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "carbonctl_confirm_stop", Arguments: map[string]any{"token": token},
	})

	if got := sender.sent(); len(got) != 0 {
		t.Fatalf("commands after elicitation timeout = %v, want none", got)
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
