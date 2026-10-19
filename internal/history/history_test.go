package history_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/carbonctl/internal/history"
	"git.kuhree.com/kuhree.com/carbonctl/internal/printer"
)

type fakeQuerier struct {
	calls   []int
	asked   [][]string
	list    []string
	details string
	err     error
}

func (fake *fakeQuerier) Query(_ context.Context, command int, data map[string]any, mainboardID string) (json.RawMessage, error) {
	fake.calls = append(fake.calls, command)
	if fake.err != nil {
		return nil, fake.err
	}
	if command == 320 {
		encoded, _ := json.Marshal(map[string]any{"Ack": 0, "HistoryData": fake.list})
		return encoded, nil
	}
	fake.asked = append(fake.asked, data["Id"].([]string))
	return json.RawMessage(fake.details), nil
}

type fakeStatus struct{ id string }

func (fake fakeStatus) Current() printer.Status { return printer.Status{MainboardID: fake.id} }

func TestClientMapsSDCPHistoryNewestFirst(t *testing.T) {
	fake := &fakeQuerier{
		list: []string{"b", "a"},
		// Details deliberately arrive in a different order than the list.
		details: `{"Ack":0,"HistoryDetailList":[
			{"TaskId":"a","TaskName":"/local/old.gcode","BeginTime":1000,"EndTime":1500,"TaskStatus":3,
			 "SliceInformation":{"total_layer_numbers":10,"print_time":5213.2}},
			{"TaskId":"b","TaskName":"/local/sub/benchy.gcode","BeginTime":2000,"EndTime":7213,"TaskStatus":1,
			 "AlreadyPrintLayer":248,"SliceInformation":{"total_layer_numbers":248}}
		]}`,
	}

	tasks, err := (history.Client{Querier: fake, Status: fakeStatus{"board-7"}}).Recent(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].TaskID != "b" || tasks[1].TaskID != "a" {
		t.Fatalf("tasks = %#v", tasks)
	}
	first := tasks[0]
	if first.Filename != "benchy.gcode" || first.Result != "completed" ||
		first.DurationSeconds != 5213 || first.TotalLayers != 248 ||
		!first.CompletedAt.Equal(time.Unix(7213, 0)) {
		t.Fatalf("task = %#v", first)
	}
	if tasks[1].Result != "stopped" || tasks[1].Filename != "old.gcode" {
		t.Fatalf("task = %#v", tasks[1])
	}
}

func TestClientPassesUnknownTaskStatusThroughUnlabeled(t *testing.T) {
	fake := &fakeQuerier{list: []string{"a"}, details: `{"HistoryDetailList":[{"TaskId":"a","TaskStatus":9}]}`}
	tasks, err := (history.Client{Querier: fake, Status: fakeStatus{"board-7"}}).Recent(context.Background(), 5)
	if err != nil || len(tasks) != 1 || tasks[0].Result != "status-9" {
		t.Fatalf("tasks = %#v, err = %v", tasks, err)
	}
}

func TestClientClampsLimit(t *testing.T) {
	ids := make([]string, 20)
	for index := range ids {
		ids[index] = string(rune('a' + index))
	}
	for _, test := range []struct{ limit, want int }{{0, 5}, {-3, 5}, {3, 3}, {99, 20}} {
		fake := &fakeQuerier{list: ids, details: `{"HistoryDetailList":[]}`}
		_, err := (history.Client{Querier: fake, Status: fakeStatus{"board-7"}}).Recent(context.Background(), test.limit)
		if err != nil || len(fake.asked) != 1 || len(fake.asked[0]) != test.want {
			t.Fatalf("limit %d: asked %v, err %v, want %d ids", test.limit, fake.asked, err, test.want)
		}
	}
}

func TestClientOnlySendsHistoryQueries(t *testing.T) {
	fake := &fakeQuerier{list: []string{"a"}, details: `{"HistoryDetailList":[]}`}
	_, _ = (history.Client{Querier: fake, Status: fakeStatus{"board-7"}}).Recent(context.Background(), 5)
	if len(fake.calls) != 2 || fake.calls[0] != 320 || fake.calls[1] != 321 {
		t.Fatalf("commands = %v", fake.calls)
	}
}

func TestClientUnavailableWithoutMainboardOrOnError(t *testing.T) {
	fake := &fakeQuerier{}
	if _, err := (history.Client{Querier: fake, Status: fakeStatus{""}}).Recent(context.Background(), 5); err != history.ErrHistoryUnavailable {
		t.Fatalf("no mainboard: error = %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("queried without a mainboard identity: %v", fake.calls)
	}
	fake.err = errors.New("boom")
	if _, err := (history.Client{Querier: fake, Status: fakeStatus{"board-7"}}).Recent(context.Background(), 5); err != history.ErrHistoryUnavailable {
		t.Fatalf("query error: error = %v", err)
	}
	if _, err := (history.Client{}).Recent(context.Background(), 5); err != history.ErrHistoryUnavailable {
		t.Fatalf("zero client: error = %v", err)
	}
}

func TestClientBoundsResponse(t *testing.T) {
	fake := &fakeQuerier{list: []string{"a"}, details: `{"HistoryDetailList":[{"TaskId":"a"}]}` + strings.Repeat(" ", 100)}
	client := history.Client{Querier: fake, Status: fakeStatus{"board-7"}, MaxResponseBytes: 64}
	if _, err := client.Recent(context.Background(), 5); err != history.ErrHistoryUnavailable {
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

func TestAttachSnapshotsBoundsVisualContextPerTask(t *testing.T) {
	records := make([]history.Snapshot, 25)
	for index := range records {
		records[index] = history.Snapshot{TaskID: "task-1"}
	}

	got := history.AttachSnapshots([]history.Task{{TaskID: "task-1"}}, records)
	if len(got[0].Snapshots) != 20 {
		t.Fatalf("snapshots = %d, want 20", len(got[0].Snapshots))
	}
}
