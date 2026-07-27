package notifications_test

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/notifications"
)

func TestTelegramDeliversEscapedHTMLPhotoCaption(t *testing.T) {
	var got map[string]string
	var photo []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/botsecret/sendPhoto" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if !strings.HasPrefix(request.Header.Get("Content-Type"), "multipart/form-data;") {
			t.Fatalf("content type = %q", request.Header.Get("Content-Type"))
		}
		reader, err := request.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		got, photo = readMultipart(t, reader)
		_, _ = io.WriteString(writer, `{"ok":true}`)
	}))
	defer server.Close()

	client := notifications.Telegram{
		BaseURL: server.URL, Token: "secret", ChatID: "42",
		HTTPClient: server.Client(),
	}
	err := client.Send(context.Background(), notifications.Message{
		Caption: "<b>Cube &amp; Gear · 20%</b>\nLayer 2 / 10",
		Image:   []byte("\xff\xd8frame\xff\xd9"),
		Silent:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["chat_id"] != "42" || got["parse_mode"] != "HTML" || got["disable_notification"] != "true" {
		t.Fatalf("fields = %#v", got)
	}
	if got["caption"] != "<b>Cube &amp; Gear · 20%</b>\nLayer 2 / 10" {
		t.Fatalf("caption = %q", got["caption"])
	}
	if string(photo) != "\xff\xd8frame\xff\xd9" {
		t.Fatalf("photo = %q", photo)
	}
}

func TestTelegramRetriesThreeTimesAfterImmediateAttempt(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(writer, "do not include this unsafe body", http.StatusBadGateway)
	}))
	defer server.Close()

	client := notifications.Telegram{
		BaseURL: server.URL, Token: "secret", ChatID: "42",
		HTTPClient: server.Client(), RetryDelay: time.Millisecond,
	}
	err := client.Send(context.Background(), notifications.Message{Caption: "hello"})
	if err == nil {
		t.Fatal("Send() error = nil")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "unsafe body") {
		t.Fatalf("error exposes credentials or response: %v", err)
	}
	if attempts.Load() != 4 {
		t.Fatalf("attempts = %d, want 4", attempts.Load())
	}
}

func TestTelegramDoesNotAcceptApplicationLevelRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `{"ok":false,"description":"unsafe response detail"}`)
	}))
	defer server.Close()
	client := notifications.Telegram{
		BaseURL: server.URL, Token: "secret", ChatID: "42",
		HTTPClient: server.Client(), RetryDelay: time.Millisecond,
	}
	err := client.Send(context.Background(), notifications.Message{Caption: "hello"})
	if err == nil {
		t.Fatal("Send() error = nil")
	}
	if strings.Contains(err.Error(), "unsafe response detail") {
		t.Fatalf("error exposes response: %v", err)
	}
}

func readMultipart(t *testing.T, reader *multipart.Reader) (map[string]string, []byte) {
	t.Helper()
	fields := make(map[string]string)
	var photo []byte
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return fields, photo
		}
		if err != nil {
			t.Fatal(err)
		}
		value, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		if part.FormName() == "photo" {
			photo = value
		} else {
			fields[part.FormName()] = string(value)
		}
	}
}
