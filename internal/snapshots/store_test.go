package snapshots_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/snapshots"
)

func TestStoreRetainsJPEGWithMetadata(t *testing.T) {
	directory := t.TempDir()
	store := snapshots.Store{Directory: directory}
	takenAt := time.Date(2026, 7, 26, 20, 0, 0, 0, time.UTC)

	saved, err := store.Save(context.Background(), []byte("\xff\xd8frame\xff\xd9"), snapshots.Metadata{
		TakenAt: takenAt, TaskID: "task-42", Event: "requested",
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.TaskID != "task-42" || saved.Event != "requested" || saved.ImageName == "" {
		t.Fatalf("saved metadata = %#v", saved)
	}
	image, err := os.ReadFile(filepath.Join(directory, saved.ImageName))
	if err != nil {
		t.Fatal(err)
	}
	if string(image) != "\xff\xd8frame\xff\xd9" {
		t.Fatalf("image = %q", image)
	}
	if _, err := os.Stat(filepath.Join(directory, saved.SidecarName)); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCleanupOnlyRemovesExpiredManagedPairs(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 7, 26, 20, 0, 0, 0, time.UTC)
	store := snapshots.Store{Directory: directory, Now: func() time.Time { return now }}
	old, err := store.Save(context.Background(), []byte("\xff\xd8old\xff\xd9"), snapshots.Metadata{
		TakenAt: now.Add(-73 * time.Hour), TaskID: "old",
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Save(context.Background(), []byte("\xff\xd8new\xff\xd9"), snapshots.Metadata{
		TakenAt: now.Add(-2 * time.Hour), TaskID: "new",
	})
	if err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(directory, "notes.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := store.Cleanup(context.Background(), 3*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{old.ImageName, old.SidecarName} {
		if _, err := os.Stat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Fatalf("expired file %q still exists", name)
		}
	}
	for _, name := range []string{current.ImageName, current.SidecarName, "notes.txt"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("retained file %q: %v", name, err)
		}
	}
}

func TestStoreListsValidMetadataAcrossRestart(t *testing.T) {
	directory := t.TempDir()
	store := snapshots.Store{Directory: directory}
	want, err := store.Save(context.Background(), []byte("\xff\xd8frame\xff\xd9"), snapshots.Metadata{
		TakenAt: time.Date(2026, 7, 26, 20, 0, 0, 0, time.UTC),
		TaskID:  "task-42",
		Event:   "requested",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "unrelated.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := (snapshots.Store{Directory: directory}).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("metadata = %#v, want %#v", got, []snapshots.Metadata{want})
	}
}
