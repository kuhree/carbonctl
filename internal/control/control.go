// Package control enforces carbonctl's narrow active-print mutation policy.
package control

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"sync"
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
)

const (
	pauseCommand  = 129
	stopCommand   = 130
	resumeCommand = 131

	defaultConfirmationTTL     = 5 * time.Minute
	defaultConfirmationTimeout = 15 * time.Second
	defaultPollInterval        = 25 * time.Millisecond
)

var (
	ErrNotPrinting         = errors.New("printer is not actively printing")
	ErrNotPaused           = errors.New("printer is not paused")
	ErrNoActiveTask        = errors.New("printer has no active task")
	ErrInvalidConfirmation = errors.New("stop confirmation is invalid or expired")
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

type PreparedStop struct {
	Token     string    `json:"token"`
	TaskID    string    `json:"task_id"`
	Filename  string    `json:"filename"`
	ExpiresAt time.Time `json:"expires_at"`
}

type confirmation struct {
	taskID    string
	expiresAt time.Time
}

type Manager struct {
	source              StatusSource
	sender              Sender
	now                 func() time.Time
	random              io.Reader
	confirmationTTL     time.Duration
	confirmationTimeout time.Duration
	pollInterval        time.Duration

	mu            sync.Mutex
	confirmations map[string]confirmation
}

type Option func(*Manager)

func WithClock(now func() time.Time) Option {
	return func(manager *Manager) { manager.now = now }
}

func WithRandom(random io.Reader) Option {
	return func(manager *Manager) { manager.random = random }
}

func WithConfirmationTimeout(timeout time.Duration) Option {
	return func(manager *Manager) { manager.confirmationTimeout = timeout }
}

func WithPollInterval(interval time.Duration) Option {
	return func(manager *Manager) { manager.pollInterval = interval }
}

func New(source StatusSource, sender Sender, options ...Option) *Manager {
	manager := &Manager{
		source: source, sender: sender, now: time.Now, random: rand.Reader,
		confirmationTTL:     defaultConfirmationTTL,
		confirmationTimeout: defaultConfirmationTimeout,
		pollInterval:        defaultPollInterval,
		confirmations:       make(map[string]confirmation),
	}
	for _, option := range options {
		option(manager)
	}
	return manager
}

func (manager *Manager) Pause(ctx context.Context) (Result, error) {
	current := manager.source.Current()
	if !current.Connected || current.State != "printing" || current.TaskID == "" {
		return Result{}, ErrNotPrinting
	}
	return manager.execute(ctx, "pause", pauseCommand, current.TaskID, func(status printer.Status) bool {
		return status.Connected && status.TaskID == current.TaskID && status.State == "printing"
	}, ErrNotPrinting, func(status printer.Status) bool {
		return status.Connected && status.TaskID == current.TaskID && status.State == "paused"
	})
}

func (manager *Manager) Resume(ctx context.Context) (Result, error) {
	current := manager.source.Current()
	if !current.Connected || current.State != "paused" || current.TaskID == "" {
		return Result{}, ErrNotPaused
	}
	return manager.execute(ctx, "resume", resumeCommand, current.TaskID, func(status printer.Status) bool {
		return status.Connected && status.TaskID == current.TaskID && status.State == "paused"
	}, ErrNotPaused, func(status printer.Status) bool {
		return status.Connected && status.TaskID == current.TaskID && status.State == "printing"
	})
}

func (manager *Manager) PrepareStop() (PreparedStop, error) {
	current := manager.source.Current()
	if !current.Connected || current.TaskID == "" ||
		(current.State != "printing" && current.State != "paused") {
		return PreparedStop{}, ErrNoActiveTask
	}
	tokenBytes := make([]byte, 32)
	if _, err := io.ReadFull(manager.random, tokenBytes); err != nil {
		return PreparedStop{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	expiresAt := manager.now().Add(manager.confirmationTTL)
	manager.mu.Lock()
	manager.confirmations[token] = confirmation{taskID: current.TaskID, expiresAt: expiresAt}
	manager.mu.Unlock()
	return PreparedStop{
		Token: token, TaskID: current.TaskID, Filename: current.Filename, ExpiresAt: expiresAt,
	}, nil
}

func (manager *Manager) ConfirmStop(ctx context.Context, token string) (Result, error) {
	manager.mu.Lock()
	prepared, ok := manager.confirmations[token]
	delete(manager.confirmations, token)
	manager.mu.Unlock()
	if !ok || manager.now().After(prepared.expiresAt) {
		return Result{}, ErrInvalidConfirmation
	}
	current := manager.source.Current()
	if !current.Connected || current.TaskID != prepared.taskID ||
		(current.State != "printing" && current.State != "paused") {
		return Result{}, ErrInvalidConfirmation
	}
	return manager.execute(ctx, "stop", stopCommand, current.TaskID, func(status printer.Status) bool {
		return status.Connected && status.TaskID == current.TaskID &&
			(status.State == "printing" || status.State == "paused")
	}, ErrInvalidConfirmation, func(status printer.Status) bool {
		return status.TaskID != current.TaskID ||
			status.State == "idle" || status.State == "stopped" || status.State == "complete"
	})
}

// DescribeStop validates a pending confirmation without consuming it so a
// transport can name the exact print in a human approval prompt.
func (manager *Manager) DescribeStop(token string) (PreparedStop, error) {
	manager.mu.Lock()
	prepared, ok := manager.confirmations[token]
	manager.mu.Unlock()
	if !ok || manager.now().After(prepared.expiresAt) {
		return PreparedStop{}, ErrInvalidConfirmation
	}
	current := manager.source.Current()
	if !current.Connected || current.TaskID != prepared.taskID ||
		(current.State != "printing" && current.State != "paused") {
		return PreparedStop{}, ErrInvalidConfirmation
	}
	return PreparedStop{
		Token: token, TaskID: prepared.taskID, Filename: current.Filename,
		ExpiresAt: prepared.expiresAt,
	}, nil
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
