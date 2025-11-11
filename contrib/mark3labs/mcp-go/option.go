package mcpgo

import (
	"reflect"

	"github.com/mark3labs/mcp-go/server"
)

// The file contains methods for easily adding tracing to a MCP server.

// Pass to server.NewMCPServer to add tracing to the server.
func WithTracing() server.ServerOption {
	return func(s *server.MCPServer) {

		// Append hooks (hooks is a private field)
		v := reflect.ValueOf(s).Elem()
		hooksField := v.FieldByName("hooks")
		if !hooksField.IsValid() {
			return
		}
		hooksField = reflect.NewAt(hooksField.Type(), hooksField.Addr().UnsafePointer()).Elem()
		if hooksField.IsNil() {
			hooksField.Set(reflect.ValueOf(&server.Hooks{}))
		}
		hooks := hooksField.Interface().(*server.Hooks)
		AddServerHooks(hooks)

		// Add tool handler middleware (toolHandlerMiddlewares is a private field, but this appensd to it)
		server.WithToolHandlerMiddleware(NewToolHandlerMiddleware())(s)
	}
}
