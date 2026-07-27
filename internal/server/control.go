package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"git.kuhree.com/kuhree.com/fleet/bin/carbonctl/internal/control"
)

type PrintController interface {
	Pause(context.Context) (control.Result, error)
	Resume(context.Context) (control.Result, error)
	PrepareStop() (control.PreparedStop, error)
	DescribeStop(string) (control.PreparedStop, error)
	ConfirmStop(context.Context, string) (control.Result, error)
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
			Name:        "carbonctl_prepare_stop",
			Description: "Prepare a task-bound stop confirmation without stopping the print",
			InputSchema: emptyInput, Call: controls.prepareStop,
		},
		{
			Name:        "carbonctl_confirm_stop",
			Description: "Stop the active print using a task-bound confirmation token",
			InputSchema: map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"token"},
				"properties": map[string]any{
					"token": map[string]any{"type": "string", "minLength": 1},
				},
			},
			Call: controls.confirmStop,
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

func (controls Controls) prepareStop(_ context.Context, arguments json.RawMessage) (any, error) {
	if !validEmptyArguments(arguments) {
		return toolError(errors.New("prepare stop accepts no arguments")), nil
	}
	result, err := controls.Controller.PrepareStop()
	if err != nil {
		return toolError(err), nil
	}
	return toolSuccess(result), nil
}

func (controls Controls) confirmStop(ctx context.Context, arguments json.RawMessage) (any, error) {
	var input struct {
		Token string `json:"token"`
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || requireEOF(decoder) != nil || input.Token == "" {
		return toolError(errors.New("confirm stop requires only a non-empty token")), nil
	}
	prepared, err := controls.Controller.DescribeStop(input.Token)
	if err != nil {
		return toolError(err), nil
	}
	filename := prepared.Filename
	if filename == "" {
		filename = prepared.TaskID
	}
	quotedFilename := strconv.Quote(filename)
	action, err := requestElicitation(ctx,
		"Stop the active print "+quotedFilename+"?",
		map[string]any{
			"type": "object", "additionalProperties": false,
		},
	)
	if err != nil {
		return toolError(errors.New("stop approval is unavailable")), nil
	}
	if action != "accept" {
		return toolError(errors.New("stop was not approved")), nil
	}
	result, err := controls.Controller.ConfirmStop(ctx, input.Token)
	return controlResult(result, err), nil
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
