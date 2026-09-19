package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/history"
	"git.kuhree.com/kuhree.com/platform/tools/carbonctl/internal/snapshots"
)

const SnapshotTimeout = 5 * time.Second

type FrameCapture interface {
	Capture(context.Context) ([]byte, error)
}

type SnapshotArchive interface {
	Save(context.Context, []byte, snapshots.Metadata) (snapshots.Metadata, error)
	List(context.Context) ([]snapshots.Metadata, error)
	Cleanup(context.Context, time.Duration) error
}

type HistorySource interface {
	Recent(context.Context, int) ([]history.Task, error)
}

type Media struct {
	Camera    FrameCapture
	Archive   SnapshotArchive
	History   HistorySource
	Status    StatusSource
	Retention time.Duration
}

func (media Media) Tools() []Tool {
	emptyInput := map[string]any{"type": "object", "additionalProperties": false}
	return []Tool{
		{
			Name: "carbonctl_get_snapshot", Description: "Capture one fresh printer image",
			InputSchema: emptyInput, Call: media.snapshot,
		},
		{
			Name: "carbonctl_get_history", Description: "Inspect recent print history",
			InputSchema: map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				},
			},
			Call: media.recent,
		},
	}
}

func (media Media) snapshot(ctx context.Context, _ json.RawMessage) (any, error) {
	if media.Camera == nil || media.Archive == nil {
		return unavailableSnapshot(), nil
	}
	captureContext, cancel := context.WithTimeout(ctx, SnapshotTimeout)
	defer cancel()
	frame, err := media.Camera.Capture(captureContext)
	if err != nil {
		return unavailableSnapshot(), nil
	}
	metadata := snapshots.Metadata{TakenAt: time.Now().UTC(), Event: "requested"}
	if media.Status != nil {
		metadata.TaskID = media.Status.Current().TaskID
	}
	if _, err := media.Archive.Save(ctx, frame, metadata); err != nil {
		return nil, err
	}
	if media.Retention > 0 {
		if err := media.Archive.Cleanup(ctx, media.Retention); err != nil {
			return nil, err
		}
	}
	structured := map[string]any{
		"availability": "available", "source": "live", "mime_type": "image/jpeg",
		"captured_at": metadata.TakenAt,
	}
	return map[string]any{
		"content": []any{map[string]any{
			"type": "image", "mimeType": "image/jpeg",
			"data": base64.StdEncoding.EncodeToString(frame),
		}},
		"structuredContent": structured, "isError": false,
	}, nil
}

func (media Media) recent(ctx context.Context, arguments json.RawMessage) (any, error) {
	if media.History == nil || media.Archive == nil {
		return unavailableHistory(), nil
	}
	var input struct {
		Limit int `json:"limit"`
	}
	if len(arguments) > 0 && string(arguments) != "null" {
		decoder := json.NewDecoder(bytes.NewReader(arguments))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || requireEOF(decoder) != nil {
			return nil, errors.New("invalid history arguments")
		}
	}
	tasks, err := media.History.Recent(ctx, input.Limit)
	if err != nil {
		return unavailableHistory(), nil
	}
	retained, err := media.Archive.List(ctx)
	if err != nil {
		return nil, err
	}
	visuals := make([]history.Snapshot, 0, len(retained))
	for _, metadata := range retained {
		visuals = append(visuals, history.Snapshot{
			TakenAt: metadata.TakenAt, TaskID: metadata.TaskID, Event: metadata.Event,
		})
	}
	tasks = history.AttachSnapshots(tasks, visuals)
	structured := map[string]any{
		"availability": "available", "source": "historical", "tasks": tasks,
	}
	return map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": mustJSON(structured)}},
		"structuredContent": structured, "isError": false,
	}, nil
}

func unavailableSnapshot() map[string]any {
	structured := map[string]any{
		"availability": "unavailable", "source": "live", "message": "Image unavailable",
	}
	return map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": "Image unavailable"}},
		"structuredContent": structured, "isError": false,
	}
}

func unavailableHistory() map[string]any {
	structured := map[string]any{
		"availability": "unavailable", "source": "historical", "tasks": []history.Task{},
	}
	return map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": "History unavailable"}},
		"structuredContent": structured, "isError": false,
	}
}
