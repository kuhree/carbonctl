package control_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/control"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
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

func TestPrepareAndConfirmStopIsTaskBoundAndSingleUse(t *testing.T) {
	now := time.Date(2026, 7, 26, 20, 0, 0, 0, time.UTC)
	statuses := &statusSource{status: printer.Status{
		Connected: true, State: "printing", TaskID: "task-42",
		Filename: "benchy.gcode",
	}}
	commands := &commandSender{afterSend: func(command int) {
		statuses.set(printer.Status{Connected: true, State: "idle"})
	}}
	manager := control.New(statuses, commands,
		control.WithClock(func() time.Time { return now }),
		control.WithRandom(strings.NewReader(strings.Repeat("unpredictable", 4))),
		control.WithConfirmationTimeout(100*time.Millisecond),
		control.WithPollInterval(time.Millisecond),
	)

	prepared, err := manager.PrepareStop()
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Token == "" || prepared.TaskID != "task-42" ||
		prepared.Filename != "benchy.gcode" ||
		!prepared.ExpiresAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("PrepareStop() = %#v", prepared)
	}
	result, err := manager.ConfirmStop(context.Background(), prepared.Token)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != control.Confirmed {
		t.Fatalf("ConfirmStop() = %#v", result)
	}
	if _, err := manager.ConfirmStop(context.Background(), prepared.Token); !errors.Is(err, control.ErrInvalidConfirmation) {
		t.Fatalf("reused ConfirmStop() error = %v, want ErrInvalidConfirmation", err)
	}
	if got := commands.sent(); len(got) != 1 || got[0] != 130 {
		t.Fatalf("commands = %#v, want one stop", got)
	}
}

func TestStopConfirmationExpiresAndRejectsAChangedTask(t *testing.T) {
	tests := []struct {
		name   string
		change func(*time.Time, *statusSource)
	}{
		{
			name: "expired",
			change: func(now *time.Time, _ *statusSource) {
				*now = now.Add(5*time.Minute + time.Nanosecond)
			},
		},
		{
			name: "changed task",
			change: func(_ *time.Time, source *statusSource) {
				source.set(printer.Status{
					Connected: true, State: "printing", TaskID: "task-99",
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 26, 20, 0, 0, 0, time.UTC)
			statuses := &statusSource{status: printer.Status{
				Connected: true, State: "printing", TaskID: "task-42",
			}}
			commands := &commandSender{}
			manager := control.New(statuses, commands,
				control.WithClock(func() time.Time { return now }),
				control.WithRandom(strings.NewReader(strings.Repeat("random", 8))),
			)
			prepared, err := manager.PrepareStop()
			if err != nil {
				t.Fatal(err)
			}
			test.change(&now, statuses)

			_, err = manager.ConfirmStop(context.Background(), prepared.Token)

			if !errors.Is(err, control.ErrInvalidConfirmation) {
				t.Fatalf("ConfirmStop() error = %v, want ErrInvalidConfirmation", err)
			}
			if commands.count() != 0 {
				t.Fatalf("sent %d commands, want none", commands.count())
			}
		})
	}
}

type statusSource struct {
	mu     sync.RWMutex
	status printer.Status
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
