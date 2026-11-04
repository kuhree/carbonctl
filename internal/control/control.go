// Package control enforces carbonctl's narrow active-print mutation policy.
package control

import (
	"context"
	"errors"
	"time"

	"git.kuhree.com/kuhree.com/carbonctl/internal/printer"
)

const (
	pauseCommand  = 129
	stopCommand   = 130
	resumeCommand = 131

	defaultConfirmationTimeout = 15 * time.Second
	defaultPollInterval        = 25 * time.Millisecond
)

var (
	ErrNotPrinting  = errors.New("printer is not actively printing")
	ErrNotPaused    = errors.New("printer is not paused")
	ErrNoActiveTask = errors.New("printer has no active task")
)

type StatusSource interface {
	Current() printer.Status
}

type Sender interface {
	Send(context.Context, int, printer.Status) error
}

type Outcome string

const (
	Confirmed           Outcome = "confirmed"
	AcceptedUnconfirmed Outcome = "accepted-unconfirmed"
)

type Result struct {
	Action  string  `json:"action"`
	Outcome Outcome `json:"outcome"`
	TaskID  string  `json:"task_id"`
}

type Manager struct {
	source              StatusSource
	sender              Sender
	confirmationTimeout time.Duration
	pollInterval        time.Duration
}

type Option func(*Manager)

func WithConfirmationTimeout(timeout time.Duration) Option {
	return func(manager *Manager) { manager.confirmationTimeout = timeout }
}

func WithPollInterval(interval time.Duration) Option {
	return func(manager *Manager) { manager.pollInterval = interval }
}

func New(source StatusSource, sender Sender, options ...Option) *Manager {
	manager := &Manager{
		source: source, sender: sender,
		confirmationTimeout: defaultConfirmationTimeout,
		pollInterval:        defaultPollInterval,
	}
	for _, option := range options {
		option(manager)
	}
	return manager
}

func (manager *Manager) Pause(ctx context.Context) (Result, error) {
	return manager.transition(ctx, "pause", pauseCommand,
		printer.StatePrinting, printer.StatePaused, ErrNotPrinting)
}

func (manager *Manager) Resume(ctx context.Context) (Result, error) {
	return manager.transition(ctx, "resume", resumeCommand,
		printer.StatePaused, printer.StatePrinting, ErrNotPaused)
}

func (manager *Manager) transition(
	ctx context.Context,
	action string,
	command int,
	from printer.State,
	to printer.State,
	preconditionError error,
) (Result, error) {
	current := manager.source.Current()
	if !current.IsTaskIn(current.TaskID, from) {
		return Result{}, preconditionError
	}
	return manager.execute(ctx, action, command, current.TaskID, func(status printer.Status) bool {
		return status.IsTaskIn(current.TaskID, from)
	}, preconditionError, func(status printer.Status) bool {
		return status.IsTaskIn(current.TaskID, to)
	})
}

func (manager *Manager) Stop(ctx context.Context) (Result, error) {
	current := manager.source.Current()
	if !current.HasActiveTask() {
		return Result{}, ErrNoActiveTask
	}
	return manager.execute(ctx, "stop", stopCommand, current.TaskID, func(status printer.Status) bool {
		return status.IsTaskIn(current.TaskID, printer.StatePrinting, printer.StatePaused)
	}, ErrNoActiveTask, func(status printer.Status) bool {
		return status.TaskEnded(current.TaskID)
	})
}

func (manager *Manager) execute(
	ctx context.Context,
	action string,
	command int,
	taskID string,
	allowed func(printer.Status) bool,
	preconditionError error,
	confirmed func(printer.Status) bool,
) (Result, error) {
	current := manager.source.Current()
	if !allowed(current) {
		return Result{}, preconditionError
	}
	if err := manager.sender.Send(ctx, command, current); err != nil {
		return Result{}, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, manager.confirmationTimeout)
	defer cancel()
	ticker := time.NewTicker(manager.pollInterval)
	defer ticker.Stop()
	for {
		if confirmed(manager.source.Current()) {
			return Result{Action: action, Outcome: Confirmed, TaskID: taskID}, nil
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			return Result{Action: action, Outcome: AcceptedUnconfirmed, TaskID: taskID}, nil
		case <-ticker.C:
		}
	}
}
