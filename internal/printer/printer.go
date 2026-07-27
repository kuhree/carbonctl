// Package printer adapts OpenCentauri SDCP telemetry into carbonctl's domain.
package printer

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/gorilla/websocket"
)

const (
	maxMessageBytes = 1 << 20
	readTimeout     = 45 * time.Second
	dialTimeout     = 500 * time.Millisecond
)

type Temperatures struct {
	Nozzle       float64 `json:"nozzle"`
	NozzleTarget float64 `json:"nozzle_target"`
	Bed          float64 `json:"bed"`
	BedTarget    float64 `json:"bed_target"`
	Chamber      float64 `json:"chamber"`
}

type Status struct {
	Connected             bool         `json:"connected"`
	State                 string       `json:"state"`
	MainboardID           string       `json:"-"`
	TaskID                string       `json:"task_id,omitempty"`
	Filename              string       `json:"filename,omitempty"`
	ProgressPercent       float64      `json:"progress_percent,omitempty"`
	CurrentLayer          int          `json:"current_layer,omitempty"`
	TotalLayers           int          `json:"total_layers,omitempty"`
	ElapsedSeconds        int          `json:"elapsed_seconds,omitempty"`
	EstimatedTotalSeconds int          `json:"estimated_total_seconds,omitempty"`
	ErrorNumber           int          `json:"error_number,omitempty"`
	Temperatures          Temperatures `json:"temperatures"`
	ObservedAt            time.Time    `json:"-"`
}

type Observer interface {
	Observe(Status) error
	Disconnected() error
}

type Client struct {
	URL      string
	Observer Observer
}

func (client Client) Run(ctx context.Context) error {
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		if err := client.read(ctx); err == nil || ctx.Err() != nil {
			return nil
		}
		if err := client.Observer.Disconnected(); err != nil {
			return err
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
	return nil
}

func (client Client) read(ctx context.Context) error {
	attemptContext, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = dialTimeout
	connection, _, err := dialer.DialContext(attemptContext, client.URL, nil)
	if err != nil {
		return err
	}
	defer connection.Close()
	connection.SetReadLimit(maxMessageBytes)
	for {
		if err := connection.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
			return err
		}
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			return err
		}
		if messageType != websocket.TextMessage {
			continue
		}
		status, err := ParseStatus(payload)
		if err != nil {
			continue
		}
		status.Connected = true
		status.ObservedAt = time.Now().UTC()
		if err := client.Observer.Observe(status); err != nil {
			return err
		}
	}
}

func ParseStatus(payload []byte) (Status, error) {
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		return Status{}, err
	}
	statusNode, ok := object(document, "Status")
	if !ok {
		statusNode = document
	}
	printNode, ok := object(statusNode, "PrintInfo")
	if !ok {
		printNode, _ = object(document, "PrintInfo")
	}
	current, ok := numbers(statusNode, "CurrentStatus")
	if !ok {
		current, ok = numbers(document, "CurrentStatus")
	}
	if !ok || len(current) == 0 {
		return Status{}, errors.New("SDCP status has no machine state")
	}
	state := stateName(int(current[0]))
	switch integer(printNode, "Status") {
	case 6:
		state = "paused"
	case 7:
		state = "stopping"
	case 8:
		state = "stopped"
	case 9:
		state = "complete"
	}
	errorNumber := integer(printNode, "ErrorNumber")
	if errorNumber != 0 {
		state = "error"
	}
	return Status{
		State:                 state,
		MainboardID:           text(document, "MainboardID"),
		TaskID:                text(printNode, "TaskId", "TaskID"),
		Filename:              text(printNode, "Filename", "FileName"),
		ProgressPercent:       number(printNode, "Progress"),
		CurrentLayer:          integer(printNode, "CurrentLayer"),
		TotalLayers:           integer(printNode, "TotalLayer", "TotalLayers"),
		ElapsedSeconds:        integer(printNode, "CurrentTicks"),
		EstimatedTotalSeconds: integer(printNode, "TotalTicks"),
		ErrorNumber:           errorNumber,
		Temperatures: Temperatures{
			Nozzle:       number(statusNode, "TempOfNozzle"),
			NozzleTarget: number(statusNode, "TempTargetNozzle"),
			Bed:          number(statusNode, "TempOfHotbed"),
			BedTarget:    number(statusNode, "TempTargetHotbed"),
			Chamber:      number(statusNode, "TempOfBox"),
		},
	}, nil
}

func stateName(code int) string {
	switch code {
	case 0:
		return "idle"
	case 1:
		return "printing"
	case 2:
		return "file-transfer"
	case 3:
		return "calibrating"
	case 4:
		return "testing"
	default:
		return "unknown"
	}
}

func object(values map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		if value, ok := values[key].(map[string]any); ok {
			return value, true
		}
	}
	return nil, false
}

func text(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok {
			return value
		}
	}
	return ""
}

func number(values map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if value, ok := values[key].(float64); ok && !math.IsNaN(value) && !math.IsInf(value, 0) {
			return value
		}
	}
	return 0
}

func integer(values map[string]any, keys ...string) int {
	return int(number(values, keys...))
}

func numbers(values map[string]any, key string) ([]float64, bool) {
	raw, ok := values[key].([]any)
	if !ok {
		return nil, false
	}
	result := make([]float64, 0, len(raw))
	for _, value := range raw {
		number, ok := value.(float64)
		if !ok {
			return nil, false
		}
		result = append(result, number)
	}
	return result, true
}
