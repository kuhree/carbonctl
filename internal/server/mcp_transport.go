package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewMCPServer adapts carbonctl tools to the official MCP transport.
func NewMCPServer(source StatusSource, tools ...Tool) *mcp.Server {
	mcpServer := mcp.NewServer(
		&mcp.Implementation{Name: "carbonctl", Version: "1"},
		nil,
	)
	status := Tool{
		Name: "carbonctl_get_status", Description: "Observe the current printer state",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false},
		Call: func(context.Context, json.RawMessage) (any, error) {
			return toolSuccess(source.Current()), nil
		},
	}
	for _, tool := range append([]Tool{status}, tools...) {
		registered := tool
		mcpServer.AddTool(&mcp.Tool{
			Name: registered.Name, Description: registered.Description,
			InputSchema: registered.InputSchema,
		}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			value, err := registered.Call(ctx, request.Params.Arguments)
			if err != nil {
				return nil, err
			}
			return convertToolResult(value)
		})
	}
	return mcpServer
}

func convertToolResult(value any) (*mcp.CallToolResult, error) {
	result, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("tool returned an invalid MCP result")
	}
	converted := &mcp.CallToolResult{
		StructuredContent: result["structuredContent"],
	}
	if isError, ok := result["isError"].(bool); ok {
		converted.IsError = isError
	}
	rawContent, ok := result["content"].([]any)
	if !ok {
		return nil, errors.New("tool returned invalid MCP content")
	}
	for _, raw := range rawContent {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("tool returned invalid MCP content item")
		}
		switch item["type"] {
		case "text":
			text, _ := item["text"].(string)
			converted.Content = append(converted.Content, &mcp.TextContent{Text: text})
		case "image":
			encoded, _ := item["data"].(string)
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, errors.New("tool returned invalid image data")
			}
			mimeType, _ := item["mimeType"].(string)
			converted.Content = append(converted.Content, &mcp.ImageContent{
				Data: data, MIMEType: mimeType,
			})
		default:
			return nil, errors.New("tool returned unsupported MCP content")
		}
	}
	return converted, nil
}
