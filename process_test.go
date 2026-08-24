package carbonctl_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/snapshots"
)

var processTestBinary string

func TestMain(main *testing.M) {
	directory, err := os.MkdirTemp("", "carbonctl-process-test-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	processTestBinary = filepath.Join(directory, "carbonctl")
	build := exec.Command("go", "build", "-o", processTestBinary, "./cmd/carbonctl")
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		fmt.Fprintf(os.Stderr, "build carbonctl process-test binary: %v\n%s", buildErr, output)
		_ = os.RemoveAll(directory)
		os.Exit(1)
	}
	code := main.Run()
	_ = os.RemoveAll(directory)
	os.Exit(code)
}

func TestProcessExposesBoundedOperationalStatus(t *testing.T) {
	address := freeAddress(t)
	output := startProcess(t, address, "ws://127.0.0.1:1/websocket", filepath.Join(t.TempDir(), "state.json"))

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

func TestSnapshotReturnsOneFreshFrameAndRetainsIt(t *testing.T) {
	const jpeg = "\xff\xd8\xff\xe0fresh-frame\xff\xd9"
	cameraRequests := make(chan struct{}, 2)
	camera := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cameraRequests <- struct{}{}
		writer.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		_, _ = io.WriteString(writer, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: 17\r\n\r\n"+jpeg+"\r\n--frame--\r\n")
	}))
	defer camera.Close()

	address := freeAddress(t)
	snapshotDir := filepath.Join(t.TempDir(), "snapshots")
	process := startProcessWithArgs(t, address,
		"-printer-url", "ws://127.0.0.1:1/websocket",
		"-state", filepath.Join(t.TempDir(), "state.json"),
		"-camera-url", camera.URL,
		"-snapshot-dir", snapshotDir,
	)
	waitForHTTP(t, "http://"+address+"/health", process)

	result := callTool(t, "http://"+address+"/mcp", "carbonctl_get_snapshot")
	structured := result["structuredContent"].(map[string]any)
	if structured["availability"] != "available" || structured["source"] != "live" {
		t.Fatalf("snapshot metadata = %#v", structured)
	}
	content := result["content"].([]any)
	image := content[0].(map[string]any)
	if image["type"] != "image" || image["mimeType"] != "image/jpeg" {
		t.Fatalf("snapshot content = %#v", content)
	}
	select {
	case <-cameraRequests:
	default:
		t.Fatal("camera was not requested")
	}
	entries, err := os.ReadDir(snapshotDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("retained entries = %d, want JPEG and JSON sidecar", len(entries))
	}
}

func TestSnapshotReportsImageUnavailable(t *testing.T) {
	camera := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "stream slot unavailable", http.StatusServiceUnavailable)
	}))
	defer camera.Close()

	address := freeAddress(t)
	process := startProcessWithArgs(t, address,
		"-printer-url", "ws://127.0.0.1:1/websocket",
		"-state", filepath.Join(t.TempDir(), "state.json"),
		"-camera-url", camera.URL,
		"-snapshot-dir", filepath.Join(t.TempDir(), "snapshots"),
	)
	waitForHTTP(t, "http://"+address+"/health", process)

	result := callTool(t, "http://"+address+"/mcp", "carbonctl_get_snapshot")
	structured := result["structuredContent"].(map[string]any)
	if structured["availability"] != "unavailable" || structured["message"] != "Image unavailable" {
		t.Fatalf("snapshot metadata = %#v", structured)
	}
}

func TestHistoryAssociatesRetainedSnapshotAfterRestart(t *testing.T) {
	const jpeg = "\xff\xd8\xff\xe0fresh-frame\xff\xd9"
	camera := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		_, _ = io.WriteString(writer, "--frame\r\nContent-Type: image/jpeg\r\n\r\n"+jpeg+"\r\n--frame--\r\n")
	}))
	defer camera.Close()
	printerURL := fakePrinter(t, `{
		"Status":{"CurrentStatus":[1],"PrintInfo":{
			"TaskId":"task-42","Filename":"cube.gcode","Progress":20
		}}
	}`)
	historyServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("limit") != "5" {
			t.Errorf("history limit = %q, want 5", request.URL.Query().Get("limit"))
		}
		_, _ = io.WriteString(writer, `{"tasks":[
			{"task_id":"task-42","filename":"cube.gcode","result":"completed","completed_at":"2026-07-26T18:00:00Z"}
		]}`)
	}))
	defer historyServer.Close()
	snapshotDir := filepath.Join(t.TempDir(), "snapshots")

	firstAddress := freeAddress(t)
	first := startProcessWithArgs(t, firstAddress,
		"-printer-url", printerURL,
		"-state", filepath.Join(t.TempDir(), "state.json"),
		"-camera-url", camera.URL,
		"-snapshot-dir", snapshotDir,
	)
	waitForHTTP(t, "http://"+firstAddress+"/health", first)
	deadline := time.Now().Add(5 * time.Second)
	for callStatus(t, "http://"+firstAddress+"/mcp")["task_id"] != "task-42" {
		if time.Now().After(deadline) {
			t.Fatal("active task did not arrive")
		}
		time.Sleep(20 * time.Millisecond)
	}
	callTool(t, "http://"+firstAddress+"/mcp", "carbonctl_get_snapshot")

	secondAddress := freeAddress(t)
	second := startProcessWithArgs(t, secondAddress,
		"-printer-url", "ws://127.0.0.1:1/websocket",
		"-state", filepath.Join(t.TempDir(), "state.json"),
		"-history-url", historyServer.URL,
		"-snapshot-dir", snapshotDir,
	)
	waitForHTTP(t, "http://"+secondAddress+"/health", second)
	result := callTool(t, "http://"+secondAddress+"/mcp", "carbonctl_get_history")
	structured := result["structuredContent"].(map[string]any)
	if structured["availability"] != "available" || structured["source"] != "historical" {
		t.Fatalf("history metadata = %#v", structured)
	}
	tasks := structured["tasks"].([]any)
	task := tasks[0].(map[string]any)
	if len(task["snapshots"].([]any)) != 1 {
		t.Fatalf("history task = %#v", task)
	}
}

func TestProcessExpiresRetainedSnapshotsAtStartup(t *testing.T) {
	snapshotDir := filepath.Join(t.TempDir(), "snapshots")
	old, err := (snapshots.Store{Directory: snapshotDir}).Save(
		context.Background(),
		[]byte("\xff\xd8old\xff\xd9"),
		snapshots.Metadata{TakenAt: time.Now().Add(-2 * time.Hour), TaskID: "old-task"},
	)
	if err != nil {
		t.Fatal(err)
	}

	address := freeAddress(t)
	process := startProcessWithArgs(t, address,
		"-printer-url", "ws://127.0.0.1:1/websocket",
		"-state", filepath.Join(t.TempDir(), "state.json"),
		"-snapshot-dir", snapshotDir,
		"-snapshot-retention", "1h",
	)
	waitForHTTP(t, "http://"+address+"/health", process)

	for _, name := range []string{old.ImageName, old.SidecarName} {
		if _, err := os.Stat(filepath.Join(snapshotDir, name)); !os.IsNotExist(err) {
			t.Fatalf("expired file %q was not removed", name)
		}
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
	return launchProcess(t,
		"-listen", address, "-printer-url", printerURL, "-state", statePath,
	).output
}

func startProcessWithArgs(t *testing.T, address string, arguments ...string) io.Reader {
	t.Helper()
	args := append([]string{"-listen", address}, arguments...)
	return launchProcess(t, args...).output
}

type testProcess struct {
	command  *exec.Cmd
	output   io.Reader
	cancel   context.CancelFunc
	stopOnce sync.Once
}

func launchProcess(t *testing.T, arguments ...string) *testProcess {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, processTestBinary, arguments...)
	output, err := command.StderrPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	process := &testProcess{command: command, output: output, cancel: cancel}
	t.Cleanup(process.stop)
	return process
}

func (process *testProcess) stop() {
	process.stopOnce.Do(func() {
		process.cancel()
		_ = process.command.Wait()
	})
}

func callTool(t *testing.T, url, name string) map[string]any {
	t.Helper()
	session := connectHTTPMCP(t, url, nil)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: name, Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return toolResultMap(t, result)
}

func callStatus(t *testing.T, url string) map[string]any {
	t.Helper()
	result := callTool(t, url, "carbonctl_get_status")
	return result["structuredContent"].(map[string]any)
}

func connectHTTPMCP(t *testing.T, url string, options *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "carbonctl-process-test", Version: "1"}, options)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: url}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func toolResultMap(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var converted map[string]any
	if err := json.Unmarshal(payload, &converted); err != nil {
		t.Fatal(err)
	}
	return converted
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

func eventuallyProcess(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not met")
}
