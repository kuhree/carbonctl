// Package camera captures bounded still frames from an MJPEG stream.
package camera

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
)

const defaultMaxFrameBytes = 8 << 20

var ErrImageUnavailable = errors.New("image unavailable")

type Client struct {
	URL           string
	HTTPClient    *http.Client
	MaxFrameBytes int64
}

func (client Client) Capture(ctx context.Context) ([]byte, error) {
	if client.URL == "" {
		return nil, ErrImageUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.URL, nil)
	if err != nil {
		return nil, ErrImageUnavailable
	}
	httpClient := client.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, ErrImageUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrImageUnavailable
	}
	mediaType, parameters, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/x-mixed-replace" || parameters["boundary"] == "" {
		return nil, ErrImageUnavailable
	}
	// Some MJPEG streamers (including this printer's) put the wire delimiter's
	// leading "--" inside the boundary parameter itself, even though the
	// multipart body only ever prepends "--" once per RFC 2046.
	boundary := strings.TrimPrefix(parameters["boundary"], "--")
	part, err := multipart.NewReader(response.Body, boundary).NextPart()
	if err != nil {
		return nil, ErrImageUnavailable
	}
	defer part.Close()
	if part.Header.Get("Content-Type") != "image/jpeg" {
		return nil, ErrImageUnavailable
	}
	maxBytes := client.MaxFrameBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxFrameBytes
	}
	frame, err := io.ReadAll(io.LimitReader(part, maxBytes+1))
	if err != nil || int64(len(frame)) > maxBytes || !isJPEG(frame) {
		return nil, ErrImageUnavailable
	}
	return frame, nil
}

func isJPEG(frame []byte) bool {
	return len(frame) >= 4 &&
		frame[0] == 0xff && frame[1] == 0xd8 &&
		frame[len(frame)-2] == 0xff && frame[len(frame)-1] == 0xd9
}
