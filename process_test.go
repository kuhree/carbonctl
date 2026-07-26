package carbonctl_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestProcessExposesBoundedOperationalStatus(t *testing.T) {
	address := freeAddress(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	command := exec.CommandContext(ctx, "go", "run", "./cmd/carbonctl",
		"-listen", address,
		"-printer-url", "ws://127.0.0.1:1/websocket",
		"-state", filepath.Join(t.TempDir(), "state.json"),
	)
	output, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = command.Wait()
	})

	waitForHTTP(t, "http://"+address+"/health", output)
	assertJSON(t, "http://"+address+"/health", map[string]any{"status": "ok"})
	assertJSON(t, "http://"+address+"/identity", map[string]any{
		"name": "carbonctl-mcp", "version": "1",
	})
	assertJSON(t, "http://"+address+"/ready", map[string]any{
		"status": "unavailable", "printer_connected": false,
	})
}

func TestActivePrintIsNormalizedThroughMCP(t *testing.T) {
	printer := fakePrinter(t, `{
		"Topic":"sdcp/status/board-1",
		"Status":{"CurrentStatus":[1],"PrintInfo":{
			"TaskId":"task-42","Filename":"calibration-cube.gcode",
			"CurrentLayer":21,"TotalLayer":100,"Progress":20,
			"CurrentTicks":600,"TotalTicks":3000
		},"TempOfNozzle":210.5,"TempTargetNozzle":215,
		"TempOfHotbed":59.5,"TempTargetHotbed":60,
		"TempOfBox":31.25}
	}`)
	address := freeAddress(t)
	process := startProcess(t, address, printer, filepath.Join(t.TempDir(), "state.json"))
	waitForHTTP(t, "http://"+address+"/health", process)

	deadline := time.Now().Add(5 * time.Second)
	for {
		got := callStatus(t, "http://"+address+"/mcp")
		if got["connected"] == true {
			want := map[string]any{
				"connected": true, "state": "printing",
				"task_id": "task-42", "filename": "calibration-cube.gcode",
				"progress_percent": float64(20), "current_layer": float64(21),
				"total_layers": float64(100), "elapsed_seconds": float64(600),
				"estimated_total_seconds": float64(3000),
				"temperatures": map[string]any{
					"nozzle": float64(210.5), "nozzle_target": float64(215),
					"bed": float64(59.5), "bed_target": float64(60),
					"chamber": float64(31.25),
				},
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("status = %#v, want %#v", got, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("printer did not connect, last status %#v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func fakePrinter(t *testing.T, message string) string {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		if err := connection.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
			return
		}
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func startProcess(t *testing.T, address, printerURL, statePath string) io.Reader {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "go", "run", "./cmd/carbonctl",
		"-listen", address, "-printer-url", printerURL, "-state", statePath,
	)
	output, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = command.Wait()
	})
	return output
}

func callStatus(t *testing.T, url string) map[string]any {
	t.Helper()
	response, err := http.Post(url, "application/json", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"carbonctl_get_status","arguments":{}}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload struct {
		Result struct {
			Structured map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload.Result.Structured
}

func waitForHTTP(t *testing.T, url string, processOutput io.Reader) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(url)
		if err == nil {
			_ = response.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	body, _ := io.ReadAll(processOutput)
	t.Fatalf("process did not become ready: %s", body)
}

func assertJSON(t *testing.T, url string, want map[string]any) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s = %#v, want %#v", url, got, want)
	}
}
