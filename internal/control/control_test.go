package control_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/carbonctl/internal/control"
	"git.kuhree.com/kuhree.com/carbonctl/internal/printer"
)

func TestPauseRequiresAnObservedPrintingTask(t *testing.T) {
	statuses := &statusSource{status: printer.Status{
		Connected: true, State: "idle",
	}}
	commands := &commandSender{}
	manager := control.New(statuses, commands)

	_, err := manager.Pause(context.Background())

	if !errors.Is(err, control.ErrNotPrinting) {
		t.Fatalf("Pause() error = %v, want ErrNotPrinting", err)
	}
	if commands.count() != 0 {
		t.Fatalf("sent %d commands, want none", commands.count())
	}
}

func TestPauseSendsOnceAndConfirmsFromTelemetry(t *testing.T) {
	statuses := &statusSource{status: printer.Status{
		Connected: true, State: "printing", TaskID: "task-42",
	}}
	commands := &commandSender{afterSend: func(command int) {
		statuses.set(printer.Status{
			Connected: true, State: "paused", TaskID: "task-42",
		})
	}}
	manager := control.New(statuses, commands,
		control.WithConfirmationTimeout(100*time.Millisecond),
		control.WithPollInterval(time.Millisecond),
	)

	result, err := manager.Pause(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != control.Confirmed || result.TaskID != "task-42" {
		t.Fatalf("Pause() = %#v", result)
	}
	if got := commands.sent(); len(got) != 1 || got[0] != 129 {
		t.Fatalf("commands = %#v, want one pause", got)
	}
}

func TestPauseRechecksStateImmediatelyBeforeSending(t *testing.T) {
	source := &changingStatusSource{statuses: []printer.Status{
		{Connected: true, State: "printing", TaskID: "task-42"},
		{Connected: true, State: "idle", TaskID: "task-42"},
	}}
	commands := &commandSender{}
	manager := control.New(source, commands)

	_, err := manager.Pause(context.Background())

	if !errors.Is(err, control.ErrNotPrinting) {
		t.Fatalf("Pause() error = %v, want ErrNotPrinting", err)
	}
	if commands.count() != 0 {
		t.Fatalf("sent %d commands, want none", commands.count())
	}
}

func TestResumeTimesOutWithoutReplayingMutation(t *testing.T) {
	statuses := &statusSource{status: printer.Status{
		Connected: true, State: "paused", TaskID: "task-42",
	}}
	commands := &commandSender{}
	manager := control.New(statuses, commands,
		control.WithConfirmationTimeout(20*time.Millisecond),
		control.WithPollInterval(time.Millisecond),
	)

	result, err := manager.Resume(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != control.AcceptedUnconfirmed {
		t.Fatalf("Resume() outcome = %q, want %q", result.Outcome, control.AcceptedUnconfirmed)
	}
	if got := commands.sent(); len(got) != 1 || got[0] != 131 {
		t.Fatalf("commands = %#v, want one resume", got)
	}
}

func TestStopSendsOnceAndConfirmsFromTelemetry(t *testing.T) {
	statuses := &statusSource{status: printer.Status{
		Connected: true, State: "printing", TaskID: "task-42",
		Filename: "benchy.gcode",
	}}
	commands := &commandSender{afterSend: func(command int) {
		statuses.set(printer.Status{Connected: true, State: "idle"})
	}}
	manager := control.New(statuses, commands,
		control.WithConfirmationTimeout(100*time.Millisecond),
		control.WithPollInterval(time.Millisecond),
	)

	result, err := manager.Stop(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "stop" || result.Outcome != control.Confirmed || result.TaskID != "task-42" {
		t.Fatalf("Stop() = %#v", result)
	}
	if got := commands.sent(); len(got) != 1 || got[0] != 130 {
		t.Fatalf("commands = %#v, want one stop", got)
	}
}

func TestStopStopsAPausedPrint(t *testing.T) {
	statuses := &statusSource{status: printer.Status{
		Connected: true, State: "paused", TaskID: "task-42",
	}}
	commands := &commandSender{afterSend: func(int) {
		statuses.set(printer.Status{Connected: true, State: "idle"})
	}}
	manager := control.New(statuses, commands,
		control.WithConfirmationTimeout(100*time.Millisecond),
		control.WithPollInterval(time.Millisecond),
	)

	if _, err := manager.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := commands.sent(); len(got) != 1 || got[0] != 130 {
		t.Fatalf("commands = %#v, want one stop", got)
	}
}

func TestStopRequiresAnObservedActiveTask(t *testing.T) {
	statuses := &statusSource{status: printer.Status{Connected: true, State: "idle"}}
	commands := &commandSender{}
	manager := control.New(statuses, commands)

	_, err := manager.Stop(context.Background())

	if !errors.Is(err, control.ErrNoActiveTask) {
		t.Fatalf("Stop() error = %v, want ErrNoActiveTask", err)
	}
	if commands.count() != 0 {
		t.Fatalf("sent %d commands, want none", commands.count())
	}
}

func TestStopRechecksTaskImmediatelyBeforeSending(t *testing.T) {
	source := &changingStatusSource{statuses: []printer.Status{
		{Connected: true, State: "printing", TaskID: "task-42"},
		{Connected: true, State: "idle"},
	}}
	commands := &commandSender{}
	manager := control.New(source, commands)

	_, err := manager.Stop(context.Background())

	if !errors.Is(err, control.ErrNoActiveTask) {
		t.Fatalf("Stop() error = %v, want ErrNoActiveTask", err)
	}
	if commands.count() != 0 {
		t.Fatalf("sent %d commands, want none", commands.count())
	}
}

type statusSource struct {
	mu     sync.RWMutex
	status printer.Status
}

type changingStatusSource struct {
	mu       sync.Mutex
	statuses []printer.Status
}

func (source *changingStatusSource) Current() printer.Status {
	source.mu.Lock()
	defer source.mu.Unlock()
	status := source.statuses[0]
	if len(source.statuses) > 1 {
		source.statuses = source.statuses[1:]
	}
	return status
}

func (source *statusSource) Current() printer.Status {
	source.mu.RLock()
	defer source.mu.RUnlock()
	return source.status
}

func (source *statusSource) set(status printer.Status) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.status = status
}

type commandSender struct {
	mu        sync.Mutex
	commands  []int
	err       error
	afterSend func(int)
}

func (sender *commandSender) Send(_ context.Context, command int, _ printer.Status) error {
	sender.mu.Lock()
	sender.commands = append(sender.commands, command)
	afterSend := sender.afterSend
	err := sender.err
	sender.mu.Unlock()
	if afterSend != nil {
		afterSend(command)
	}
	return err
}

func (sender *commandSender) sent() []int {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return append([]int(nil), sender.commands...)
}

func (sender *commandSender) count() int {
	return len(sender.sent())
}
