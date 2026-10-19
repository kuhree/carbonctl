package printer_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/carbonctl/internal/printer"
)

func TestQuerierSendsReadOnlyHistoryQuery(t *testing.T) {
	var received map[string]any
	url := fakeCommandPrinter(t, func(payload []byte) int {
		_ = json.Unmarshal(payload, &received)
		return 0
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	reply, err := (printer.Querier{URL: url}).Query(ctx, 321, map[string]any{"Id": []string{"a"}}, "board-7")
	if err != nil {
		t.Fatal(err)
	}
	data := received["Data"].(map[string]any)
	if received["Topic"] != "sdcp/request/board-7" || data["Cmd"] != float64(321) ||
		data["From"] != float64(1) || data["MainboardID"] != "board-7" ||
		len(data["RequestID"].(string)) != 32 { // firmware mangles dashed 36-char IDs
		t.Fatalf("request = %#v", received)
	}
	if string(reply) != `{"Ack":0}` {
		t.Fatalf("reply = %s", reply)
	}
}

func TestQuerierRejectsEverythingButHistoryQueries(t *testing.T) {
	url := fakeCommandPrinter(t, func([]byte) int {
		t.Error("a rejected command reached the printer")
		return 0
	})
	for _, command := range []int{0, 1, 128, 129, 130, 131, 258, 259, 319, 322, 403} {
		_, err := (printer.Querier{URL: url}).Query(context.Background(), command, nil, "board-7")
		if err != printer.ErrUnsupportedCommand {
			t.Fatalf("Query(%d) error = %v, want unsupported", command, err)
		}
	}
}

func TestQuerierFailsOnNonZeroAck(t *testing.T) {
	url := fakeCommandPrinter(t, func([]byte) int { return 1 })
	_, err := (printer.Querier{URL: url}).Query(context.Background(), 320, nil, "board-7")
	if err == nil {
		t.Fatal("error = nil, want failure")
	}
}

func TestQuerierNeedsMainboardID(t *testing.T) {
	_, err := (printer.Querier{URL: "ws://127.0.0.1:1"}).Query(context.Background(), 320, nil, "")
	if err != printer.ErrMissingMainboardID {
		t.Fatalf("error = %v", err)
	}
}

// The control path must never carry the read-only queries, and must keep
// refusing every command outside pause/resume/stop.
func TestCommanderRejectsQueriesAndAllOtherCommands(t *testing.T) {
	url := fakeCommandPrinter(t, func([]byte) int {
		t.Error("a rejected command reached the printer")
		return 0
	})
	status := printer.Status{MainboardID: "board-7"}
	for _, command := range []int{0, 1, 128, 132, 133, 192, 255, 258, 259, 320, 321, 386, 387, 403} {
		err := (printer.Commander{URL: url}).Send(context.Background(), command, status)
		if err != printer.ErrUnsupportedCommand {
			t.Fatalf("Send(%d) error = %v, want unsupported", command, err)
		}
	}
}
