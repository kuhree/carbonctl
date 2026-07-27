// Package snapshots retains bounded still images and their metadata.
package snapshots

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const filePrefix = "carbonctl-snapshot-"

type Metadata struct {
	TakenAt     time.Time `json:"taken_at"`
	TaskID      string    `json:"task_id,omitempty"`
	Event       string    `json:"event,omitempty"`
	ImageName   string    `json:"image_name"`
	SidecarName string    `json:"sidecar_name"`
}

type Store struct {
	Directory string
	Now       func() time.Time
}

func (store Store) List(ctx context.Context) ([]Metadata, error) {
	if store.Directory == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(store.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []Metadata
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() || !managedName(entry.Name(), ".json") {
			continue
		}
		payload, err := os.ReadFile(filepath.Join(store.Directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		var metadata Metadata
		if json.Unmarshal(payload, &metadata) != nil ||
			metadata.SidecarName != entry.Name() ||
			!managedName(metadata.ImageName, ".jpg") {
			continue
		}
		if _, err := os.Stat(filepath.Join(store.Directory, metadata.ImageName)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		result = append(result, metadata)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].TakenAt.After(result[right].TakenAt)
	})
	return result, nil
}

func (store Store) Save(ctx context.Context, frame []byte, metadata Metadata) (Metadata, error) {
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	if store.Directory == "" {
		return Metadata{}, errors.New("snapshot directory is not configured")
	}
	if metadata.TakenAt.IsZero() {
		metadata.TakenAt = store.now()
	}
	suffix, err := randomSuffix()
	if err != nil {
		return Metadata{}, err
	}
	base := filePrefix + metadata.TakenAt.UTC().Format("20060102T150405.000000000Z") + "-" + suffix
	metadata.ImageName = base + ".jpg"
	metadata.SidecarName = base + ".json"
	if err := os.MkdirAll(store.Directory, 0o700); err != nil {
		return Metadata{}, err
	}
	imagePath := filepath.Join(store.Directory, metadata.ImageName)
	if err := atomicWrite(imagePath, frame); err != nil {
		return Metadata{}, err
	}
	sidecar, err := json.Marshal(metadata)
	if err != nil {
		_ = os.Remove(imagePath)
		return Metadata{}, err
	}
	if err := atomicWrite(filepath.Join(store.Directory, metadata.SidecarName), sidecar); err != nil {
		_ = os.Remove(imagePath)
		return Metadata{}, err
	}
	return metadata, nil
}

func (store Store) Cleanup(ctx context.Context, retention time.Duration) error {
	if store.Directory == "" || retention <= 0 {
		return nil
	}
	entries, err := os.ReadDir(store.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cutoff := store.now().Add(-retention)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), filePrefix) || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		sidecarPath := filepath.Join(store.Directory, entry.Name())
		payload, err := os.ReadFile(sidecarPath)
		if err != nil {
			return err
		}
		var metadata Metadata
		if json.Unmarshal(payload, &metadata) != nil ||
			!metadata.TakenAt.Before(cutoff) ||
			metadata.SidecarName != entry.Name() ||
			!managedName(metadata.ImageName, ".jpg") {
			continue
		}
		imagePath := filepath.Join(store.Directory, metadata.ImageName)
		if err := os.Remove(imagePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Remove(sidecarPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (store Store) now() time.Time {
	if store.Now != nil {
		return store.Now().UTC()
	}
	return time.Now().UTC()
}

func managedName(name, extension string) bool {
	return filepath.Base(name) == name && strings.HasPrefix(name, filePrefix) && filepath.Ext(name) == extension
}

func randomSuffix() (string, error) {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func atomicWrite(path string, payload []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".carbonctl-snapshot-*")
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
	return os.Rename(temporaryPath, path)
}
