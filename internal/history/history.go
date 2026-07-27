// Package history provides a bounded printer-facing print history seam.
package history

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	defaultLimit       = 5
	maxLimit           = 20
	defaultMaxResponse = 1 << 20
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

type Client struct {
	URL              string
	HTTPClient       *http.Client
	MaxResponseBytes int64
}

func (client Client) Recent(ctx context.Context, limit int) ([]Task, error) {
	limit = boundedLimit(limit)
	endpoint, err := url.Parse(client.URL)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, ErrHistoryUnavailable
	}
	query := endpoint.Query()
	query.Set("limit", strconv.Itoa(limit))
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, ErrHistoryUnavailable
	}
	httpClient := client.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, ErrHistoryUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrHistoryUnavailable
	}
	maxBytes := client.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxResponse
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || int64(len(payload)) > maxBytes {
		return nil, ErrHistoryUnavailable
	}
	var document struct {
		Tasks []Task `json:"tasks"`
	}
	if json.Unmarshal(payload, &document) != nil {
		return nil, ErrHistoryUnavailable
	}
	if len(document.Tasks) > limit {
		document.Tasks = document.Tasks[:limit]
	}
	return document.Tasks, nil
}

func AttachSnapshots(tasks []Task, snapshots []Snapshot) []Task {
	result := append([]Task(nil), tasks...)
	for index := range result {
		for _, snapshot := range snapshots {
			if snapshot.TaskID == result[index].TaskID {
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
