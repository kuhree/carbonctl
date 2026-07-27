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

func (configuration options) tool(name string) (Tool, bool) {
	for _, tool := range configuration.tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return Tool{}, false
}
