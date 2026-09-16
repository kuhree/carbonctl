// Package state owns carbonctl's concurrency-safe current observation.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"git.kuhree.com/kuhree.com/platform/bin/carbonctl/internal/printer"
)

type Store struct {
	mu             sync.RWMutex
	path           string
	current        printer.Status
	milestones     map[string]map[int]bool
	disconnectedAt time.Time
}

type document struct {
	Version         int              `json:"version"`
	Current         printer.Status   `json:"current"`
	LastObservation time.Time        `json:"last_observation,omitempty"`
	SentMilestones  map[string][]int `json:"sent_milestones"`
	DisconnectedAt  time.Time        `json:"disconnected_at,omitempty"`
}

func Open(path string) (*Store, error) {
	store := &Store{path: path, milestones: make(map[string]map[int]bool)}
	if path == "" {
		return store, nil
	}
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var saved document
	if err := json.Unmarshal(payload, &saved); err != nil {
		return nil, errors.New("carbonctl state is corrupt")
	}
	if saved.Version != 1 {
		return nil, errors.New("carbonctl state version is unsupported")
	}
	store.current = saved.Current
	store.current.Connected = false
	store.current.ObservedAt = saved.LastObservation
	store.disconnectedAt = saved.DisconnectedAt
	for taskID, values := range saved.SentMilestones {
		store.milestones[taskID] = make(map[int]bool)
		for _, value := range values {
			store.milestones[taskID][value] = true
		}
	}
	return store, nil
}

func (store *Store) Observe(status printer.Status) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	document := store.documentLocked()
	document.Current = status
	document.LastObservation = status.ObservedAt
	document.DisconnectedAt = time.Time{}
	if err := store.persistLocked(document); err != nil {
		return err
	}
	store.current, store.disconnectedAt = status, time.Time{}
	return nil
}

func (store *Store) Disconnected() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	current := store.current
	current.Connected = false
	disconnectedAt := store.disconnectedAt
	if disconnectedAt.IsZero() {
		disconnectedAt = time.Now().UTC()
	}
	document := store.documentLocked()
	document.Current = current
	document.DisconnectedAt = disconnectedAt
	if err := store.persistLocked(document); err != nil {
		return err
	}
	store.current, store.disconnectedAt = current, disconnectedAt
	return nil
}

func (store *Store) Current() printer.Status {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.current
}

func (store *Store) MarkMilestone(taskID string, milestone int) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	document := store.documentLocked()
	values := append(document.SentMilestones[taskID], milestone)
	document.SentMilestones[taskID] = values
	if err := store.persistLocked(document); err != nil {
		return err
	}
	if store.milestones[taskID] == nil {
		store.milestones[taskID] = make(map[int]bool)
	}
	store.milestones[taskID][milestone] = true
	return nil
}

func (store *Store) MilestoneSent(taskID string, milestone int) bool {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.milestones[taskID][milestone]
}

func (store *Store) DisconnectedAt(at time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	current := store.current
	current.Connected = false
	document := store.documentLocked()
	document.Current, document.DisconnectedAt = current, at
	if err := store.persistLocked(document); err != nil {
		return err
	}
	store.current, store.disconnectedAt = current, at
	return nil
}

func (store *Store) LastDisconnectedAt() time.Time {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.disconnectedAt
}

func (store *Store) documentLocked() document {
	milestones := make(map[string][]int, len(store.milestones))
	for taskID, values := range store.milestones {
		for milestone := range values {
			milestones[taskID] = append(milestones[taskID], milestone)
		}
	}
	return document{
		Version: 1, Current: store.current, LastObservation: store.current.ObservedAt,
		SentMilestones: milestones, DisconnectedAt: store.disconnectedAt,
	}
}

func (store *Store) persistLocked(saved document) error {
	if store.path == "" {
		return nil
	}
	payload, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(store.path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".carbonctl-state-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, store.path); err != nil {
		return err
	}
	directoryHandle, err := os.Open(filepath.Dir(store.path))
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}
