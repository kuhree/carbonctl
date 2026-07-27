package carbonctl_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestProcessSendsLifecyclePhotosWithoutDuplicates(t *testing.T) {
	telemetry := make(chan string, 4)
	upgrader := websocket.Upgrader{}
	printer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		for {
			select {
			case message := <-telemetry:
				if connection.WriteMessage(websocket.TextMessage, []byte(message)) != nil {
					return
				}
			case <-request.Context().Done():
				return
			}
		}
	}))
	defer printer.Close()

	camera := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		const jpeg = "\xff\xd8notification-frame\xff\xd9"
		writer.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		_, _ = io.WriteString(writer, "--frame\r\nContent-Type: image/jpeg\r\n\r\n"+jpeg+"\r\n--frame--\r\n")
	}))
	defer camera.Close()

	type delivered struct {
		caption string
		silent  string
	}
	var mu sync.Mutex
	var deliveries []delivered
	telegram := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/bottest-token/sendPhoto" {
			t.Errorf("Telegram path = %q", request.URL.Path)
		}
		fields, _, err := processMultipart(request)
		if err != nil {
			t.Errorf("parse Telegram request: %v", err)
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		deliveries = append(deliveries, delivered{fields["caption"], fields["disable_notification"]})
		mu.Unlock()
		_, _ = io.WriteString(writer, `{"ok":true}`)
	}))
	defer telegram.Close()

	secrets := t.TempDir()
	tokenPath := filepath.Join(secrets, "token")
	chatPath := filepath.Join(secrets, "chat")
	if err := os.WriteFile(tokenPath, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chatPath, []byte("42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	address := freeAddress(t)
	snapshotDirectory := filepath.Join(t.TempDir(), "snapshots")
	process := startProcessWithArgs(t, address,
		"-printer-url", "ws"+strings.TrimPrefix(printer.URL, "http"),
		"-state", filepath.Join(t.TempDir(), "state.json"),
		"-camera-url", camera.URL,
		"-snapshot-dir", snapshotDirectory,
		"-telegram-api-url", telegram.URL,
		"-telegram-token-file", tokenPath,
		"-telegram-chat-file", chatPath,
	)
	waitForHTTP(t, "http://"+address+"/health", process)

	telemetry <- telemetryJSON("idle", 0)
	telemetry <- telemetryJSON("printing", 1)
	telemetry <- telemetryJSON("printing", 20)
	telemetry <- telemetryJSON("printing", 20)

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		count := len(deliveries)
		mu.Unlock()
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("deliveries = %d, want 2", count)
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(deliveries) != 2 {
		t.Fatalf("deliveries = %#v", deliveries)
	}
	if deliveries[0].silent != "false" || deliveries[1].silent != "true" {
		t.Fatalf("urgency = %#v", deliveries)
	}
	if !strings.Contains(deliveries[1].caption, "<b>cube.gcode · 20%</b>") {
		t.Fatalf("caption = %q", deliveries[1].caption)
	}
	entries, err := os.ReadDir(snapshotDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("retained snapshot files = %d, want two JPEG/JSON pairs", len(entries))
	}
}

func processMultipart(request *http.Request) (map[string]string, []byte, error) {
	reader, err := request.MultipartReader()
	if err != nil {
		return nil, nil, err
	}
	fields := make(map[string]string)
	var image []byte
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return fields, image, nil
		}
		if err != nil {
			return nil, nil, err
		}
		value, err := io.ReadAll(part)
		if err != nil {
			return nil, nil, err
		}
		if part.FormName() == "photo" {
			image = value
		} else {
			fields[part.FormName()] = string(value)
		}
	}
}

func telemetryJSON(state string, progress int) string {
	code, printStatus := 0, 0
	if state == "printing" {
		code = 1
	}
	if state == "complete" {
		code, printStatus = 1, 9
	}
	return fmt.Sprintf(`{"Status":{"CurrentStatus":[%d],
		"PrintInfo":{"TaskId":"task-1","Filename":"cube.gcode","Progress":%d,
		"CurrentLayer":2,"TotalLayer":10,"CurrentTicks":60,"TotalTicks":600,"Status":%d},
		"TempOfNozzle":210,"TempOfHotbed":60,"TempOfBox":30}}`, code, progress, printStatus)
}
