// Package history provides a bounded printer-facing print history seam.
package history

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strconv"
	"strings"
	"time"

	"git.kuhree.com/kuhree.com/carbonctl/internal/printer"
)

const (
	defaultLimit        = 5
	maxLimit            = 20
	maxSnapshotsPerTask = 20
	defaultMaxResponse  = 1 << 20
)

var ErrHistoryUnavailable = errors.New("history unavailable")

type Snapshot struct {
	TakenAt time.Time `json:"taken_at"`
	TaskID  string    `json:"task_id"`
	Event   string    `json:"event,omitempty"`
}

type Task struct {
	TaskID          string     `json:"task_id"`
	Filename        string     `json:"filename,omitempty"`
	Result          string     `json:"result,omitempty"`
	CompletedAt     time.Time  `json:"completed_at,omitempty"`
	DurationSeconds int        `json:"duration_seconds,omitempty"`
	TotalLayers     int        `json:"total_layers,omitempty"`
	Snapshots       []Snapshot `json:"snapshots,omitempty"`
}

// Querier is the printer's read-only SDCP query seam (printer.Querier).
type Querier interface {
	Query(ctx context.Context, command int, data map[string]any, mainboardID string) (json.RawMessage, error)
}

// StatusSource supplies the printer's mainboard identity (state.Store).
type StatusSource interface {
	Current() printer.Status
}

// Client reads print history over SDCP: command 320 lists recent task IDs,
// command 321 returns their details.
type Client struct {
	Querier          Querier
	Status           StatusSource
	MaxResponseBytes int64
}

const (
	listCommand   = printer.QueryHistoryList
	detailCommand = printer.QueryHistoryDetail
)

func (client Client) Recent(ctx context.Context, limit int) ([]Task, error) {
	limit = boundedLimit(limit)
	if client.Querier == nil || client.Status == nil {
		return nil, ErrHistoryUnavailable
	}
	mainboardID := client.Status.Current().MainboardID
	if mainboardID == "" {
		return nil, ErrHistoryUnavailable
	}
	maxBytes := client.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxResponse
	}

	reply, err := client.Querier.Query(ctx, listCommand, map[string]any{}, mainboardID)
	if err != nil || int64(len(reply)) > maxBytes {
		return nil, ErrHistoryUnavailable
	}
	var list struct {
		HistoryData []string `json:"HistoryData"`
	}
	if json.Unmarshal(reply, &list) != nil {
		return nil, ErrHistoryUnavailable
	}
	ids := list.HistoryData
	if len(ids) > limit {
		ids = ids[:limit]
	}
	if len(ids) == 0 {
		return []Task{}, nil
	}

	reply, err = client.Querier.Query(ctx, detailCommand, map[string]any{"Id": ids}, mainboardID)
	if err != nil || int64(len(reply)) > maxBytes {
		return nil, ErrHistoryUnavailable
	}
	var details struct {
		HistoryDetailList []detail `json:"HistoryDetailList"`
	}
	if json.Unmarshal(reply, &details) != nil {
		return nil, ErrHistoryUnavailable
	}
	byID := make(map[string]detail, len(details.HistoryDetailList))
	for _, item := range details.HistoryDetailList {
		byID[item.TaskID] = item
	}
	tasks := make([]Task, 0, len(ids))
	for _, id := range ids { // keep the printer's newest-first order
		if item, ok := byID[id]; ok {
			tasks = append(tasks, item.task())
		}
	}
	return tasks, nil
}

type detail struct {
	TaskID            string `json:"TaskId"`
	TaskName          string `json:"TaskName"`
	BeginTime         int64  `json:"BeginTime"`
	EndTime           int64  `json:"EndTime"`
	TaskStatus        int    `json:"TaskStatus"`
	AlreadyPrintLayer int    `json:"AlreadyPrintLayer"`
	SliceInformation  struct {
		TotalLayers int `json:"total_layer_numbers"`
	} `json:"SliceInformation"`
}

func (item detail) task() Task {
	task := Task{
		TaskID:      item.TaskID,
		Filename:    path.Base(strings.TrimPrefix(item.TaskName, "/local/")),
		Result:      resultName(item.TaskStatus),
		TotalLayers: item.SliceInformation.TotalLayers,
	}
	if item.TaskName == "" {
		task.Filename = ""
	}
	if item.EndTime > 0 {
		task.CompletedAt = time.Unix(item.EndTime, 0).UTC()
		if item.BeginTime > 0 && item.EndTime > item.BeginTime {
			task.DurationSeconds = int(item.EndTime - item.BeginTime)
		}
	}
	return task
}

// resultName labels TaskStatus using the OpenCentauri SDCP docs
// (docs.opencentauri.cc/software/api): 0 other, 1 completed, 2 exceptional,
// 3 stopped. Any other value is passed through as its number, unlabeled.
func resultName(status int) string {
	switch status {
	case 0:
		return "other"
	case 1:
		return "completed"
	case 2:
		return "exceptional"
	case 3:
		return "stopped"
	default:
		return "status-" + strconv.Itoa(status)
	}
}

func AttachSnapshots(tasks []Task, snapshots []Snapshot) []Task {
	result := append([]Task(nil), tasks...)
	for index := range result {
		for _, snapshot := range snapshots {
			if snapshot.TaskID == result[index].TaskID &&
				len(result[index].Snapshots) < maxSnapshotsPerTask {
				result[index].Snapshots = append(result[index].Snapshots, snapshot)
			}
		}
	}
	return result
}

func boundedLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}
