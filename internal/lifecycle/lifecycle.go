// Package lifecycle turns observed printer transitions into notifications.
package lifecycle

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/notifications"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
)

const (
	StartMilestone      = 0
	completeMilestone   = 100
	pauseMilestone      = 101
	errorMilestone      = 102
	disconnectMilestone = 103
	recoveryMilestone   = 104
	disconnectDelay     = 2 * time.Minute
	maxCaptionBytes     = 1024
)

var progressMilestones = []int{20, 40, 60, 80}

type Store interface {
	Observe(printer.Status) error
	Disconnected() error
	DisconnectedAt(time.Time) error
	Current() printer.Status
	LastDisconnectedAt() time.Time
	MarkMilestone(string, int) error
	MilestoneSent(string, int) bool
}

type Snapshotter interface {
	Capture(context.Context, printer.Status, string) ([]byte, error)
}

type Sender interface {
	Send(context.Context, notifications.Message) error
}

type Engine struct {
	mu              sync.Mutex
	Store           Store
	Snapshots       Snapshotter
	Sender          Sender
	Now             func() time.Time
	DisconnectDelay time.Duration
	initialized     bool
	previous        printer.Status
	pending         []event
}

type event struct {
	name      string
	key       string
	milestone int
	status    printer.Status
	silent    bool
}

func New(store Store, snapshots Snapshotter, sender Sender) *Engine {
	previous := store.Current()
	return &Engine{
		Store: store, Snapshots: snapshots, Sender: sender,
		initialized: observed(previous), previous: previous,
	}
}

func (engine *Engine) Observe(ctx context.Context, status printer.Status) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	previous := engine.previous
	disconnectedAt := engine.Store.LastDisconnectedAt()
	if err := engine.Store.Observe(status); err != nil {
		return err
	}
	if !engine.initialized {
		engine.initialized = true
		engine.previous = status
		if status.State == printer.StateComplete {
			engine.pending = append(engine.pending, taskEvent("complete", completeMilestone, status, false))
		} else if status.State == printer.StateError {
			engine.pending = append(engine.pending, taskEvent("error", errorMilestone, status, false))
		} else {
			if err := engine.reconcileProgress(status, true); err != nil {
				return err
			}
		}
		return engine.flush(ctx)
	}
	if status.State == printer.StatePrinting && (!previous.Connected ||
		(previous.TaskID == status.TaskID &&
			previous.State == printer.StatePrinting &&
			previous.ProgressPercent == status.ProgressPercent)) {
		if err := engine.reconcileProgress(status, previous.TaskID == status.TaskID); err != nil {
			return err
		}
	}

	if !disconnectedAt.IsZero() {
		key := connectionKey(disconnectedAt)
		if engine.Store.MilestoneSent(key, disconnectMilestone) {
			engine.pending = append(engine.pending, event{
				name: "recovery", key: key, milestone: recoveryMilestone, status: status,
			})
		}
	}
	if status.State == printer.StatePrinting &&
		(previous.TaskID != status.TaskID || previous.State != printer.StatePrinting) {
		engine.pending = append(engine.pending, taskEvent("start", StartMilestone, status, false))
	}
	if status.TaskID != "" && status.TaskID == previous.TaskID && previous.Connected {
		for _, milestone := range progressMilestones {
			if previous.ProgressPercent < float64(milestone) && status.ProgressPercent >= float64(milestone) {
				engine.pending = append(engine.pending, taskEvent(
					fmt.Sprintf("progress-%d", milestone), milestone, status, true,
				))
			}
		}
	}
	if status.State == printer.StatePaused && previous.State != printer.StatePaused {
		engine.pending = append(engine.pending, taskEvent("pause", pauseMilestone, status, false))
	}
	if status.State == printer.StateComplete && previous.State != printer.StateComplete {
		engine.pending = append(engine.pending, taskEvent("complete", completeMilestone, status, false))
	}
	if status.State == printer.StateError && previous.State != printer.StateError {
		engine.pending = append(engine.pending, taskEvent("error", errorMilestone, status, false))
	}
	engine.previous = status
	return engine.flush(ctx)
}

func (engine *Engine) Disconnected() error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	at := engine.now()
	if existing := engine.Store.LastDisconnectedAt(); !existing.IsZero() {
		at = existing
	}
	if err := engine.Store.DisconnectedAt(at); err != nil {
		return err
	}
	engine.previous = engine.Store.Current()
	return nil
}

func (engine *Engine) CheckDisconnect(ctx context.Context) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	at := engine.Store.LastDisconnectedAt()
	delay := engine.DisconnectDelay
	if delay <= 0 {
		delay = disconnectDelay
	}
	if at.IsZero() || engine.now().Sub(at) < delay {
		return nil
	}
	key := connectionKey(at)
	engine.pending = append(engine.pending, event{
		name: "disconnect", key: key, milestone: disconnectMilestone,
		status: engine.Store.Current(),
	})
	return engine.flush(ctx)
}

func (engine *Engine) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := engine.CheckDisconnect(ctx); err != nil {
				return err
			}
		}
	}
}

type Observer struct {
	Context context.Context
	Engine  *Engine
}

func (observer Observer) Observe(status printer.Status) error {
	return observer.Engine.Observe(observer.Context, status)
}

func (observer Observer) Disconnected() error {
	return observer.Engine.Disconnected()
}

func (engine *Engine) flush(ctx context.Context) error {
	for len(engine.pending) > 0 {
		next := engine.pending[0]
		if engine.Store.MilestoneSent(next.key, next.milestone) {
			engine.pending = engine.pending[1:]
			continue
		}
		message := notifications.Message{Caption: caption(next), Silent: next.silent}
		if engine.Snapshots != nil {
			image, err := engine.Snapshots.Capture(ctx, next.status, next.name)
			if err == nil {
				message.Image = image
			} else {
				slog.Warn("lifecycle snapshot capture failed", "event", next.name, "error", err)
				message.Caption = boundedCaption(message.Caption + "\n📷 Image unavailable")
			}
		}
		if err := engine.Sender.Send(ctx, message); err != nil {
			return err
		}
		if err := engine.Store.MarkMilestone(next.key, next.milestone); err != nil {
			return err
		}
		engine.pending = engine.pending[1:]
	}
	return nil
}

func (engine *Engine) reconcileProgress(status printer.Status, markStart bool) error {
	if status.TaskID == "" || status.State != printer.StatePrinting {
		return nil
	}
	if markStart && !engine.Store.MilestoneSent(status.TaskID, StartMilestone) {
		if err := engine.Store.MarkMilestone(status.TaskID, StartMilestone); err != nil {
			return err
		}
	}
	for _, milestone := range progressMilestones {
		if status.ProgressPercent >= float64(milestone) && !engine.Store.MilestoneSent(status.TaskID, milestone) {
			if err := engine.Store.MarkMilestone(status.TaskID, milestone); err != nil {
				return err
			}
		}
	}
	return nil
}

func taskEvent(name string, milestone int, status printer.Status, silent bool) event {
	return event{name: name, key: status.TaskID, milestone: milestone, status: status, silent: silent}
}

func caption(value event) string {
	status := value.status
	name := status.Filename
	if name == "" {
		name = "Printer"
	}
	heading := eventHeading(value.name)
	progress := fmt.Sprintf("%.0f%%", status.ProgressPercent)
	lines := []string{
		fmt.Sprintf("🖨️ <b>%s · %s</b>", safeName(name), progress),
		fmt.Sprintf("%s · %s", heading, html.EscapeString(string(status.State))),
		fmt.Sprintf("Layer %d / %d", status.CurrentLayer, status.TotalLayers),
		fmt.Sprintf("⏱️ %s remaining", remaining(status)),
		fmt.Sprintf("🌡️ Nozzle %.0f° · Bed %.0f° · Chamber %.0f°",
			status.Temperatures.Nozzle, status.Temperatures.Bed, status.Temperatures.Chamber),
	}
	return boundedCaption(strings.Join(lines, "\n"))
}

func safeName(value string) string {
	runes := []rune(value)
	if len(runes) > 100 {
		value = string(runes[:99]) + "…"
	}
	return html.EscapeString(value)
}

func eventHeading(name string) string {
	switch {
	case name == "start":
		return "Started"
	case strings.HasPrefix(name, "progress-"):
		return "Progress"
	case name == "complete":
		return "Complete"
	case name == "pause":
		return "Paused"
	case name == "error":
		return "⚠️ Error"
	case name == "disconnect":
		return "⚠️ Disconnected"
	case name == "recovery":
		return "Monitoring restored"
	default:
		return name
	}
}

func remaining(status printer.Status) string {
	seconds := status.EstimatedTotalSeconds - status.ElapsedSeconds
	if seconds <= 0 {
		return "unknown"
	}
	duration := time.Duration(seconds) * time.Second
	if duration >= time.Hour {
		return fmt.Sprintf("%dh %dm", int(duration.Hours()), int(duration.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(duration.Minutes()))
}

func boundedCaption(value string) string {
	if len(value) <= maxCaptionBytes {
		return value
	}
	value = value[:maxCaptionBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func connectionKey(at time.Time) string {
	return fmt.Sprintf("connection:%d", at.UnixNano())
}

func observed(status printer.Status) bool {
	return !status.ObservedAt.IsZero() || status.TaskID != "" || status.State != ""
}

func (engine *Engine) now() time.Time {
	if engine.Now != nil {
		return engine.Now().UTC()
	}
	return time.Now().UTC()
}
