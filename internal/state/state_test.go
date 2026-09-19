package state_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/printer"
	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/state"
)

func TestStateSurvivesRestartWithNotificationBookkeeping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	observedAt := time.Date(2026, 7, 26, 22, 30, 0, 0, time.UTC)
	disconnectedAt := observedAt.Add(time.Minute)
	store, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Observe(printer.Status{
		Connected: true, State: "printing", TaskID: "task-42",
		Filename: "cube.gcode", ObservedAt: observedAt,
	})
	store.MarkMilestone("task-42", 20)
	store.DisconnectedAt(disconnectedAt)

	reopened, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Current(); got.TaskID != "task-42" || got.ObservedAt != observedAt {
		t.Fatalf("reopened current status = %#v", got)
	}
	if !reopened.MilestoneSent("task-42", 20) {
		t.Fatal("reopened state forgot sent milestone")
	}
	if got := reopened.LastDisconnectedAt(); !got.Equal(disconnectedAt) {
		t.Fatalf("disconnected at = %v, want %v", got, disconnectedAt)
	}
}

func TestFailedPersistenceDoesNotMarkMilestoneAsDurable(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(directory, "state.json")
	store, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Observe(printer.Status{TaskID: "task-42"})
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory, []byte("blocks state directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := store.MarkMilestone("task-42", 20); err == nil {
		t.Fatal("MarkMilestone succeeded despite an unwritable state path")
	}
	if store.MilestoneSent("task-42", 20) {
		t.Fatal("failed milestone write changed in-memory durable state")
	}
}

func TestReopenedStateDoesNotClaimALiveConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Observe(printer.Status{
		Connected: true, State: "printing", TaskID: "task-42",
		ObservedAt: time.Now().UTC(),
	})

	reopened, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Current().Connected {
		t.Fatal("persisted telemetry was presented as a live connection")
	}
}
