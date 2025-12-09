# MCP Go-SDK Integration

This integration provides Datadog tracing for the [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) library.

## Usage

```go
import (
    gosdktrace "github.com/DataDog/dd-trace-go/contrib/modelcontextprotocol/go-sdk/v2"
    "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
    "github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	tracer.Start()
	defer tracer.Stop()

	server := mcp.NewServer(&mcp.Implementation{Name: "my-server", Version: "1.0.0"}, nil)
	gosdktrace.AddTracingMiddleware(server)
}
```

## Features

The integration automatically traces:
- **Tool calls**: Creates LLMObs tool spans with input/output annotation for all tool invocations
- **Session initialization**: Creates LLMObs task spans for session initialization, including client information

