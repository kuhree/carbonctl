package lifecycle_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/lifecycle"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/notifications"
	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
)

func TestLifecycleNotifiesNewPrintAndProgressOnce(t *testing.T) {
	store := newMemoryStore()
	var sent []notifications.Message
	var events []string
	engine := lifecycle.New(store,
		snapshotFunc(func(_ context.Context, _ printer.Status, event string) ([]byte, error) {
			events = append(events, event)
			return []byte("jpeg"), nil
		}),
		senderFunc(func(_ context.Context, message notifications.Message) error {
			sent = append(sent, message)
			return nil
		}),
	)
	idle := status("idle", "", 0)
	if err := engine.Observe(context.Background(), idle); err != nil {
		t.Fatal(err)
	}
	printing := status("printing", "cube<&>.gcode", 1)
	if err := engine.Observe(context.Background(), printing); err != nil {
		t.Fatal(err)
	}
	printing.ProgressPercent = 20
	printing.CurrentLayer, printing.TotalLayers = 2, 10
	if err := engine.Observe(context.Background(), printing); err != nil {
		t.Fatal(err)
	}
	if err := engine.Observe(context.Background(), printing); err != nil {
		t.Fatal(err)
	}

	if len(sent) != 2 || events[0] != "start" || events[1] != "progress-20" {
		t.Fatalf("events = %#v, messages = %d", events, len(sent))
	}
	if sent[0].Silent {
		t.Fatal("start notification is silent")
	}
	if !sent[1].Silent {
		t.Fatal("progress notification is not silent")
	}
	if !strings.Contains(sent[1].Caption, "<b>cube&lt;&amp;&gt;.gcode · 20%</b>") ||
		!strings.Contains(sent[1].Caption, "Layer 2 / 10") ||
		!strings.Contains(sent[1].Caption, "Nozzle 210° · Bed 60° · Chamber 30°") {
		t.Fatalf("caption = %q", sent[1].Caption)
	}
}

func TestLifecycleStillNotifiesWhenImageIsUnavailable(t *testing.T) {
	store := newMemoryStore()
	var got notifications.Message
	engine := lifecycle.New(store,
		snapshotFunc(func(context.Context, printer.Status, string) ([]byte, error) {
			return nil, errors.New("camera failed")
		}),
		senderFunc(func(_ context.Context, message notifications.Message) error {
			got = message
			return nil
		}),
	)
	_ = engine.Observe(context.Background(), status("idle", "", 0))
	if err := engine.Observe(context.Background(), status("printing", "cube.gcode", 1)); err != nil {
		t.Fatal(err)
	}
	if len(got.Image) != 0 || !strings.Contains(got.Caption, "Image unavailable") {
		t.Fatalf("message = %#v", got)
	}
}

func TestLifecycleBoundsCaptionWithoutBreakingHTML(t *testing.T) {
	store := newMemoryStore()
	var got notifications.Message
	engine := lifecycle.New(store, snapshotFunc(nil), senderFunc(func(_ context.Context, message notifications.Message) error {
		got = message
		return nil
	}))
	_ = engine.Observe(context.Background(), status("idle", "", 0))
	active := status("printing", strings.Repeat("<&", 2_000), 1)
	if err := engine.Observe(context.Background(), active); err != nil {
		t.Fatal(err)
	}
	if len(got.Caption) > 1024 {
		t.Fatalf("caption bytes = %d", len(got.Caption))
	}
	if strings.Count(got.Caption, "<b>") != 1 || strings.Count(got.Caption, "</b>") != 1 {
		t.Fatalf("caption has broken HTML: %q", got.Caption)
	}
}

func TestLifecycleMarksMilestoneOnlyAfterDelivery(t *testing.T) {
	store := newMemoryStore()
	attempts := 0
	engine := lifecycle.New(store, snapshotFunc(nil), senderFunc(func(context.Context, notifications.Message) error {
		attempts++
		if attempts == 1 {
			return errors.New("telegram down")
		}
		return nil
	}))
	_ = engine.Observe(context.Background(), status("idle", "", 0))
	active := status("printing", "cube.gcode", 1)
	if err := engine.Observe(context.Background(), active); err == nil {
		t.Fatal("first Observe() error = nil")
	}
	if store.MilestoneSent("task-1", lifecycle.StartMilestone) {
		t.Fatal("failed notification was recorded")
	}
	if err := engine.Observe(context.Background(), active); err != nil {
		t.Fatal(err)
	}
	if !store.MilestoneSent("task-1", lifecycle.StartMilestone) {
		t.Fatal("successful notification was not recorded")
	}
}

func TestLifecycleDoesNotBackfillProgressOnStartup(t *testing.T) {
	store := newMemoryStore()
	store.current = status("printing", "cube.gcode", 65)
	var captions []string
	engine := lifecycle.New(store, snapshotFunc(nil), senderFunc(func(_ context.Context, message notifications.Message) error {
		captions = append(captions, message.Caption)
		return nil
	}))
	if err := engine.Observe(context.Background(), store.current); err != nil {
		t.Fatal(err)
	}
	if len(captions) != 0 {
		t.Fatalf("startup captions = %#v", captions)
	}
	for _, milestone := range []int{0, 20, 40, 60} {
		if !store.MilestoneSent("task-1", milestone) {
			t.Fatalf("milestone %d was not reconciled", milestone)
		}
	}
}

func TestLifecycleDoesNotBackfillProgressAfterReconnect(t *testing.T) {
	store := newMemoryStore()
	store.current = status("printing", "cube.gcode", 20)
	engine := lifecycle.New(store, snapshotFunc(nil), senderFunc(func(context.Context, notifications.Message) error {
		t.Fatal("reconnected progress was notified")
		return nil
	}))
	if err := engine.Disconnected(); err != nil {
		t.Fatal(err)
	}
	reconnected := status("printing", "cube.gcode", 65)
	if err := engine.Observe(context.Background(), reconnected); err != nil {
		t.Fatal(err)
	}
	for _, milestone := range []int{0, 20, 40, 60} {
		if !store.MilestoneSent("task-1", milestone) {
			t.Fatalf("milestone %d was not reconciled", milestone)
		}
	}
}

func TestLifecycleReportsCompletionAndErrorFoundAfterRestart(t *testing.T) {
	for _, state := range []printer.State{printer.StateComplete, printer.StateError} {
		t.Run(string(state), func(t *testing.T) {
			store := newMemoryStore()
			store.current = status("printing", "cube.gcode", 80)
			var sent int
			engine := lifecycle.New(store, snapshotFunc(nil), senderFunc(func(context.Context, notifications.Message) error {
				sent++
				return nil
			}))
			next := store.current
			next.State = state
			if err := engine.Observe(context.Background(), next); err != nil {
				t.Fatal(err)
			}
			if sent != 1 {
				t.Fatalf("notifications = %d, want 1", sent)
			}
		})
	}
}

func TestLifecycleDebouncesDisconnectAndReportsRecovery(t *testing.T) {
	store := newMemoryStore()
	now := time.Date(2026, 7, 26, 20, 0, 0, 0, time.UTC)
	var captions []string
	engine := lifecycle.New(store, snapshotFunc(nil), senderFunc(func(_ context.Context, message notifications.Message) error {
		captions = append(captions, message.Caption)
		return nil
	}))
	engine.Now = func() time.Time { return now }
	if err := engine.Observe(context.Background(), status("printing", "cube.gcode", 40)); err != nil {
		t.Fatal(err)
	}
	if err := engine.Disconnected(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(119 * time.Second)
	if err := engine.CheckDisconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(captions) != 0 {
		t.Fatalf("early notifications = %#v", captions)
	}
	now = now.Add(time.Second)
	if err := engine.CheckDisconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := engine.CheckDisconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := status("printing", "cube.gcode", 41)
	recovered.ObservedAt = now
	if err := engine.Observe(context.Background(), recovered); err != nil {
		t.Fatal(err)
	}
	if len(captions) != 2 ||
		!strings.Contains(captions[0], "Disconnected") ||
		!strings.Contains(captions[1], "Monitoring restored") {
		t.Fatalf("captions = %#v", captions)
	}
}

type memoryStore struct {
	current      printer.Status
	disconnected time.Time
	sent         map[string]map[int]bool
}

func newMemoryStore() *memoryStore {
	return &memoryStore{sent: make(map[string]map[int]bool)}
}

func (store *memoryStore) Observe(status printer.Status) error {
	store.current, store.disconnected = status, time.Time{}
	return nil
}
func (store *memoryStore) DisconnectedAt(at time.Time) error {
	store.current.Connected, store.disconnected = false, at
	return nil
}
func (store *memoryStore) Current() printer.Status       { return store.current }
func (store *memoryStore) LastDisconnectedAt() time.Time { return store.disconnected }
func (store *memoryStore) MarkMilestone(task string, milestone int) error {
	if store.sent[task] == nil {
		store.sent[task] = make(map[int]bool)
	}
	store.sent[task][milestone] = true
	return nil
}
func (store *memoryStore) MilestoneSent(task string, milestone int) bool {
	return store.sent[task][milestone]
}

type senderFunc func(context.Context, notifications.Message) error

func (function senderFunc) Send(ctx context.Context, message notifications.Message) error {
	return function(ctx, message)
}

type snapshotFunc func(context.Context, printer.Status, string) ([]byte, error)

func (function snapshotFunc) Capture(ctx context.Context, status printer.Status, event string) ([]byte, error) {
	if function == nil {
		return []byte("jpeg"), nil
	}
	return function(ctx, status, event)
}

func status(state printer.State, filename string, progress float64) printer.Status {
	return printer.Status{
		Connected: true, State: state, TaskID: "task-1", Filename: filename,
		ProgressPercent: progress, CurrentLayer: 1, TotalLayers: 10,
		ElapsedSeconds: 60, EstimatedTotalSeconds: 600,
		Temperatures: printer.Temperatures{Nozzle: 210, Bed: 60, Chamber: 30},
		ObservedAt:   time.Date(2026, 7, 26, 20, 0, 0, 0, time.UTC),
	}
}
