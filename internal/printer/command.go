package printer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

const commandTimeout = 5 * time.Second

var (
	ErrCommandRejected    = errors.New("printer rejected command")
	ErrUnsupportedCommand = errors.New("unsupported printer command")
	ErrMissingMainboardID = errors.New("printer mainboard identity is unavailable")
)

// Commander sends one allowlisted SDCP mutation and never replays it.
type Commander struct {
	URL string
}

func (commander Commander) Send(ctx context.Context, command int, status Status) error {
	if command != 129 && command != 130 && command != 131 {
		return ErrUnsupportedCommand
	}
	if status.MainboardID == "" {
		return ErrMissingMainboardID
	}
	requestID, err := randomUUID()
	if err != nil {
		return err
	}
	messageID, err := randomUUID()
	if err != nil {
		return err
	}

	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = dialTimeout
	connection, _, err := dialer.DialContext(commandCtx, commander.URL, nil)
	if err != nil {
		return err
	}
	defer connection.Close()
	connection.SetReadLimit(maxMessageBytes)
	deadline, _ := commandCtx.Deadline()
	if err := connection.SetWriteDeadline(deadline); err != nil {
		return err
	}
	request := map[string]any{
		"Id": messageID,
		"Data": map[string]any{
			"Cmd": command, "Data": map[string]any{}, "RequestID": requestID,
			"MainboardID": status.MainboardID, "TimeStamp": time.Now().Unix(), "From": 0,
		},
		"Topic": "sdcp/request/" + status.MainboardID,
	}
	if err := connection.WriteJSON(request); err != nil {
		return err
	}
	if err := connection.SetReadDeadline(deadline); err != nil {
		return err
	}
	for {
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			return err
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var response struct {
			Topic string `json:"Topic"`
			Data  struct {
				Cmd       int    `json:"Cmd"`
				RequestID string `json:"RequestID"`
				Data      struct {
					Ack *int `json:"Ack"`
				} `json:"Data"`
			} `json:"Data"`
		}
		if err := json.Unmarshal(payload, &response); err != nil {
			continue
		}
		if response.Topic != "sdcp/response/"+status.MainboardID ||
			response.Data.RequestID != requestID ||
			response.Data.Cmd != command {
			continue
		}
		if response.Data.Data.Ack == nil || *response.Data.Data.Ack != 0 {
			return fmt.Errorf("%w: acknowledgement %v", ErrCommandRejected, response.Data.Data.Ack)
		}
		return nil
	}
}

func randomUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded), nil
}
