# Google GenAI Integration

This integration provides Datadog LLM Observability tracing for the
[google.golang.org/genai](https://github.com/googleapis/go-genai) SDK.

## Usage

```go
import (
    "context"

    genaitrace "github.com/DataDog/dd-trace-go/contrib/googleapis/go-genai/v2"
    "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
    "google.golang.org/genai"
)

func main() {
    tracer.Start()
    defer tracer.Stop()

    raw, err := genai.NewClient(context.Background(), &genai.ClientConfig{
        APIKey:  "...",
        Backend: genai.BackendGeminiAPI,
    })
    if err != nil {
        panic(err)
    }
    client := genaitrace.WrapClient(raw)

    resp, err := client.Models.GenerateContent(
        context.Background(),
        "gemini-2.0-flash",
        []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Hello!"}}}},
        nil,
    )
    _ = resp
    _ = err
}
```

The returned `*Client` exposes the same `Models` and `Chats` services as the
upstream SDK. For services that are not (yet) instrumented, call `Raw()` to get
the underlying `*genai.Client`.

## Features

The integration automatically produces LLM Observability spans for:

- **`Models.GenerateContent`** — LLM span with prompt/response messages,
  generation parameters (temperature, top_p, top_k, max_output_tokens, stop
  sequences) and token usage metrics.
- **`Models.GenerateContentStream`** — LLM span covering the entire stream;
  output text is reassembled from chunks and final usage metadata is captured.
- **`Models.EmbedContent`** — Embedding span with input documents and a summary
  of the returned vectors.
- **`Chats.Create` / `Chat.SendMessage` / `Chat.SendMessageStream`** — LLM
  spans whose input includes the existing chat history plus the new user
  message.

Spans are tagged with the model provider derived from the underlying client's
backend (`google` for Gemini API, `google_vertexai` for Vertex AI,
`google_enterprise` for the Enterprise Agent Platform).
