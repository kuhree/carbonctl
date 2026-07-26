// Package state owns carbonctl's concurrency-safe current observation.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/printer"
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

func (store *Store) Observe(status printer.Status) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.current = status
	store.disconnectedAt = time.Time{}
	store.persistLocked()
}

func (store *Store) Disconnected() {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.current.Connected = false
	if store.disconnectedAt.IsZero() {
		store.disconnectedAt = time.Now().UTC()
	}
	store.persistLocked()
}

func (store *Store) Current() printer.Status {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.current
}

func (store *Store) MarkMilestone(taskID string, milestone int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.milestones[taskID] == nil {
		store.milestones[taskID] = make(map[int]bool)
	}
	store.milestones[taskID][milestone] = true
	store.persistLocked()
}

func (store *Store) MilestoneSent(taskID string, milestone int) bool {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.milestones[taskID][milestone]
}

func (store *Store) DisconnectedAt(at time.Time) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.current.Connected = false
	store.disconnectedAt = at
	store.persistLocked()
}

func (store *Store) LastDisconnectedAt() time.Time {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.disconnectedAt
}

func (store *Store) persistLocked() {
	if store.path == "" {
		return
	}
	milestones := make(map[string][]int, len(store.milestones))
	for taskID, values := range store.milestones {
		for milestone := range values {
			milestones[taskID] = append(milestones[taskID], milestone)
		}
	}
	payload, err := json.Marshal(document{
		Version: 1, Current: store.current, LastObservation: store.current.ObservedAt,
		SentMilestones: milestones,
		DisconnectedAt: store.disconnectedAt,
	})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(store.path), 0o700); err != nil {
		return
	}
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".carbonctl-state-*")
	if err != nil {
		return
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return
	}
	if err := temporary.Close(); err != nil {
		return
	}
	_ = os.Rename(temporaryPath, store.path)
}
