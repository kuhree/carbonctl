package server

import (
	"context"
	"encoding/json"
)

type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Call        func(context.Context, json.RawMessage) (any, error)
}

func WithTools(tools ...Tool) Option {
	return func(configuration *options) {
		configuration.tools = append(configuration.tools, tools...)
	}
}
