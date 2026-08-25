package server_test

import (
	"context"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/control"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestControlToolsExposeOnlyTheApprovedMutationBoundary(t *testing.T) {
	source := &mutableStatus{status: printer.Status{
		Connected: true, State: "printing", TaskID: "task-42",
	}}
	controller := control.New(source, &observingSender{source: source},
		control.WithConfirmationTimeout(100*time.Millisecond),
		control.WithPollInterval(time.Millisecond),
	)
	session := connectMCP(t, source, controller, nil)

	listed, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	want := []string{
		"carbonctl_confirm_stop",
		"carbonctl_get_status",
		"carbonctl_pause",
		"carbonctl_prepare_stop",
		"carbonctl_resume",
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

func TestConfirmStopRejectsUnknownArgumentsWithoutSending(t *testing.T) {
	source := &mutableStatus{status: printer.Status{
		Connected: true, State: "printing", TaskID: "task-42",
	}}
	sender := &observingSender{source: source}
	controller := control.New(source, sender)
	session := connectMCP(t, source, controller, nil)

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "carbonctl_confirm_stop",
		Arguments: map[string]any{"token": "x", "extra": true},
	})
	if err == nil && !result.IsError {
		t.Fatalf("result = %#v, want rejection", result)
	}
	if len(sender.sent()) != 0 {
		t.Fatalf("commands = %v, want none", sender.sent())
	}
}
