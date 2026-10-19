package printer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Read-only SDCP query commands. Commander (the control path) never sends
// these, and Querier never sends anything Commander may send.
const (
	QueryHistoryList   = 320
	QueryHistoryDetail = 321
)

var ErrQueryFailed = errors.New("printer query failed")

// Querier sends one allowlisted read-only SDCP query and returns the reply's
// inner Data object. It cannot send pause, resume, stop or anything else.
type Querier struct {
	URL string
}

func isQuery(command int) bool {
	return command == QueryHistoryList || command == QueryHistoryDetail
}

func (querier Querier) Query(ctx context.Context, command int, data map[string]any, mainboardID string) (json.RawMessage, error) {
	if !isQuery(command) {
		return nil, ErrUnsupportedCommand
	}
	if mainboardID == "" {
		return nil, ErrMissingMainboardID
	}
	if data == nil {
		data = map[string]any{}
	}
	// The firmware echoes a 36-character dashed RequestID with a stray
	// trailing \u0001, so the reply would never match. A 32-character hex ID
	// round-trips exactly.
	requestID, err := randomUUID()
	if err != nil {
		return nil, err
	}
	requestID = strings.ReplaceAll(requestID, "-", "")
	messageID, err := randomUUID()
	if err != nil {
		return nil, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = dialTimeout
	connection, _, err := dialer.DialContext(queryCtx, querier.URL, nil)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	connection.SetReadLimit(maxMessageBytes)
	deadline, _ := queryCtx.Deadline()
	if err := connection.SetWriteDeadline(deadline); err != nil {
		return nil, err
	}
	if err := connection.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	request := map[string]any{
		"Id": messageID,
		"Data": map[string]any{
			"Cmd": command, "Data": data, "RequestID": requestID,
			"MainboardID": mainboardID, "TimeStamp": time.Now().Unix(), "From": 1,
		},
		"Topic": "sdcp/request/" + mainboardID,
	}
	if err := connection.WriteJSON(request); err != nil {
		return nil, err
	}
	for {
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			return nil, err
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var response struct {
			Topic string `json:"Topic"`
			Data  struct {
				Cmd       int             `json:"Cmd"`
				RequestID string          `json:"RequestID"`
				Data      json.RawMessage `json:"Data"`
			} `json:"Data"`
		}
		if json.Unmarshal(payload, &response) != nil {
			continue
		}
		if response.Topic != "sdcp/response/"+mainboardID ||
			response.Data.RequestID != requestID ||
			response.Data.Cmd != command {
			continue
		}
		var ack struct {
			Ack *int `json:"Ack"`
		}
		if json.Unmarshal(response.Data.Data, &ack) != nil || ack.Ack == nil || *ack.Ack != 0 {
			return nil, fmt.Errorf("%w: acknowledgement", ErrQueryFailed)
		}
		return response.Data.Data, nil
	}
}
