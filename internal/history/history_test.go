package history_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/history"
)

func TestClientReturnsFiveRecentTasksByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"tasks":[
			{"task_id":"6","filename":"six.gcode","result":"completed","completed_at":"2026-07-26T18:00:00Z"},
			{"task_id":"5","filename":"five.gcode","result":"completed","completed_at":"2026-07-26T17:00:00Z"},
			{"task_id":"4","filename":"four.gcode","result":"failed","completed_at":"2026-07-26T16:00:00Z"},
			{"task_id":"3","filename":"three.gcode","result":"completed","completed_at":"2026-07-26T15:00:00Z"},
			{"task_id":"2","filename":"two.gcode","result":"completed","completed_at":"2026-07-26T14:00:00Z"},
			{"task_id":"1","filename":"one.gcode","result":"completed","completed_at":"2026-07-26T13:00:00Z"}
		]}`)
	}))
	defer server.Close()

	tasks, err := (history.Client{URL: server.URL}).Recent(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 5 || tasks[0].TaskID != "6" || tasks[4].TaskID != "2" {
		t.Fatalf("tasks = %#v", tasks)
	}
}

func TestClientBoundsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"tasks":[{"task_id":"1"}]}`+string(make([]byte, 100)))
	}))
	defer server.Close()

	_, err := (history.Client{URL: server.URL, MaxResponseBytes: 32}).Recent(context.Background(), 5)
	if err != history.ErrHistoryUnavailable {
		t.Fatalf("error = %v, want history unavailable", err)
	}
}

func TestAttachSnapshotsAddsOnlyMatchingVisualContext(t *testing.T) {
	tasks := []history.Task{{TaskID: "task-1"}, {TaskID: "task-2"}}
	records := []history.Snapshot{
		{TaskID: "task-1", TakenAt: time.Date(2026, 7, 26, 18, 0, 0, 0, time.UTC)},
		{TaskID: "another-task", TakenAt: time.Date(2026, 7, 26, 19, 0, 0, 0, time.UTC)},
	}

	got := history.AttachSnapshots(tasks, records)
	if len(got[0].Snapshots) != 1 || len(got[1].Snapshots) != 0 {
		t.Fatalf("tasks = %#v", got)
	}
}
