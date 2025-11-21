# MCP-Go Integration

This integration provides Datadog tracing for the [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) library.

Both hooks and middleware are used.

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

    // Do not use with `server.WithHooks(...)`, as this overwrites the tracing hooks. 
    // To add custom hooks alongside tracing, pass them via TracingConfig.Hooks, e.g.:
    // mcpgotrace.WithMCPServerTracing(&mcpgotrace.TracingConfig{Hooks: customHooks})
    srv := server.NewMCPServer("my-server", "1.0.0",
		mcpgotrace.WithMCPServerTracing(nil))
}
```

## Features

The integration automatically traces:
- **Tool calls**: Creates LLMObs tool spans with input/output annotation for all tool invocations
- **Session initialization**: Create LLMObs task spans for session initialization, including client information.