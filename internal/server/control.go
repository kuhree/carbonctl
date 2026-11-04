package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"git.kuhree.com/kuhree.com/carbonctl/internal/control"
)

type PrintController interface {
	Pause(context.Context) (control.Result, error)
	Resume(context.Context) (control.Result, error)
	Stop(context.Context) (control.Result, error)
}

type Controls struct {
	Controller PrintController
}

func (controls Controls) Tools() []Tool {
	emptyInput := map[string]any{"type": "object", "additionalProperties": false}
	return []Tool{
		{
			Name: "carbonctl_pause", Description: "Pause the observed active print",
			InputSchema: emptyInput, Call: controls.transition("pause", controls.Controller.Pause),
		},
		{
			Name: "carbonctl_resume", Description: "Resume the observed paused print",
			InputSchema: emptyInput, Call: controls.transition("resume", controls.Controller.Resume),
		},
		{
			Name: "carbonctl_stop", Description: "Stop the observed active print",
			InputSchema: emptyInput, Call: controls.transition("stop", controls.Controller.Stop),
		},
	}
}

func (controls Controls) transition(
	action string,
	transition func(context.Context) (control.Result, error),
) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, arguments json.RawMessage) (any, error) {
		if !validEmptyArguments(arguments) {
			return toolError(errors.New(action + " accepts no arguments")), nil
		}
		result, err := transition(ctx)
		return controlResult(result, err), nil
	}
}

func controlResult(result control.Result, err error) any {
	if err != nil {
		return toolError(err)
	}
	return toolSuccess(result)
}

func toolSuccess(value any) map[string]any {
	return map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": mustJSON(value)}},
		"structuredContent": value, "isError": false,
	}
}

func toolError(err error) map[string]any {
	message := err.Error()
	return map[string]any{
		"content":           []any{map[string]any{"type": "text", "text": message}},
		"structuredContent": map[string]any{"error": message}, "isError": true,
	}
}

func validEmptyArguments(arguments json.RawMessage) bool {
	if len(arguments) == 0 || string(arguments) == "null" {
		return true
	}
	var input struct{}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	return decoder.Decode(&input) == nil && requireEOF(decoder) == nil
}
