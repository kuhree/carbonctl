package camera_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/camera"
)

func TestCaptureReadsOneJPEGFrame(t *testing.T) {
	const frame = "\xff\xd8camera-frame\xff\xd9"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		_, _ = io.WriteString(writer,
			"--frame\r\nContent-Type: image/jpeg\r\n\r\n"+frame+
				"\r\n--frame\r\nContent-Type: image/jpeg\r\n\r\nignored\r\n")
	}))
	defer server.Close()

	got, err := (camera.Client{URL: server.URL}).Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != frame {
		t.Fatalf("frame = %q, want %q", got, frame)
	}
}

func TestCaptureRejectsOversizedFrame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		_, _ = io.WriteString(writer, "--frame\r\nContent-Type: image/jpeg\r\n\r\n12345\r\n")
	}))
	defer server.Close()

	_, err := (camera.Client{URL: server.URL, MaxFrameBytes: 4}).Capture(context.Background())
	if err != camera.ErrImageUnavailable {
		t.Fatalf("error = %v, want image unavailable", err)
	}
}
