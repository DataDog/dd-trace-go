# MCP-Go Integration

This integration provides Datadog tracing for the [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) library.

## Usage

```go
import (
    mcpgotrace "github.com/DataDog/dd-trace-go/contrib/mark3labs/mcp-go/v2"
    "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
    "github.com/mark3labs/mcp-go/server"
)

func main() {
	tracer.Start()
	defer tracer.Stop()

	srv := server.NewMCPServer("my-server", "1.0.0",
		server.WithToolHandlerMiddleware(mcpgotrace.NewToolHandlerMiddleware()))
	_ = srv
}
```

## Features

The integration automatically traces:
- **Tool calls**: Creates LLMObs tool spans with input/output annotation for all tool invocations
