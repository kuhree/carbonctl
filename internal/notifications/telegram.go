// Package notifications delivers deterministic outbound printer messages.
package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL    = "https://api.telegram.org"
	defaultRetryDelay = 200 * time.Millisecond
	maxResponseBytes  = 4 << 10
)

type Message struct {
	Caption string
	Image   []byte
	Silent  bool
}

type Telegram struct {
	BaseURL    string
	Token      string
	ChatID     string
	HTTPClient *http.Client
	RetryDelay time.Duration
}

func (telegram Telegram) Send(ctx context.Context, message Message) error {
	var last error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			delay := telegram.RetryDelay
			if delay <= 0 {
				delay = defaultRetryDelay
			}
			timer := time.NewTimer(delay * time.Duration(attempt))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := telegram.sendOnce(ctx, message); err == nil {
			return nil
		} else {
			last = err
		}
	}
	return last
}

func (telegram Telegram) sendOnce(ctx context.Context, message Message) error {
	method := "sendMessage"
	var body io.Reader
	var contentType string
	if len(message.Image) > 0 {
		method = "sendPhoto"
		buffer := new(bytes.Buffer)
		writer := multipart.NewWriter(buffer)
		for key, value := range telegram.fields(message) {
			if err := writer.WriteField(key, value); err != nil {
				return errors.New("build Telegram request")
			}
		}
		part, err := writer.CreateFormFile("photo", "printer.jpg")
		if err != nil {
			return errors.New("build Telegram request")
		}
		if _, err := part.Write(message.Image); err != nil {
			return errors.New("build Telegram request")
		}
		if err := writer.Close(); err != nil {
			return errors.New("build Telegram request")
		}
		body, contentType = buffer, writer.FormDataContentType()
	} else {
		values := url.Values{}
		for key, value := range telegram.fields(message) {
			values.Set(key, value)
		}
		values.Set("text", message.Caption)
		values.Del("caption")
		body, contentType = strings.NewReader(values.Encode()), "application/x-www-form-urlencoded"
	}
	baseURL := strings.TrimRight(telegram.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/bot%s/%s", baseURL, telegram.Token, method), body)
	if err != nil {
		return errors.New("build Telegram request")
	}
	request.Header.Set("Content-Type", contentType)
	client := telegram.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("Telegram request failed")
	}
	defer response.Body.Close()
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Telegram rejected request with status %d", response.StatusCode)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if readErr != nil || len(payload) > maxResponseBytes || json.Unmarshal(payload, &result) != nil || !result.OK {
		return errors.New("Telegram rejected request")
	}
	return nil
}

func (telegram Telegram) fields(message Message) map[string]string {
	return map[string]string{
		"chat_id":              telegram.ChatID,
		"caption":              message.Caption,
		"parse_mode":           "HTML",
		"disable_notification": strconv.FormatBool(message.Silent),
	}
}
