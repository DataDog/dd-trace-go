# OpenFeature Provider Data Sources

The Datadog OpenFeature provider supports two data sources for fetching feature flag configurations:

1. **Remote Config** (default) - Fetches configurations through the Datadog Agent
2. **Flag Rules Backend** - Fetches configurations directly from an HTTP endpoint

## Using Remote Config (Default)

Remote Config is the default data source. It requires the Datadog Agent to be running and the tracer to be started:

```go
import (
    "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
    "github.com/DataDog/dd-trace-go/v2/openfeature"
    of "github.com/open-feature/go-sdk/openfeature"
)

// Start the tracer (required for Remote Config)
tracer.Start()
defer tracer.Stop()

// Create provider with default Remote Config
provider, err := openfeature.NewDatadogProvider(openfeature.ProviderConfig{})
if err != nil {
    log.Fatal(err)
}
defer provider.Shutdown()

of.SetProviderAndWait(provider)
```

## Switching to Flag Rules Backend

The flag rules backend fetches configurations directly from an HTTP endpoint, bypassing the Datadog Agent. This is useful for:

- Environments where the Datadog Agent isn't available
- Using a CDN or custom endpoint for flag configurations
- Testing with a local configuration server

### Option 1: Configure via Environment Variables

Set the endpoint URL via environment variable:

```bash
export DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED=true
export DD_FFE_FLAG_RULES_URL=https://your-endpoint.example.com/flags.json
```

Then create the provider with the flag rules data source:

```go
provider, err := openfeature.NewDatadogProvider(openfeature.ProviderConfig{
    DataSource: openfeature.DataSourceFlagRules,
})
```

### Option 2: Configure Programmatically

Pass the configuration directly (takes precedence over environment variables):

```go
provider, err := openfeature.NewDatadogProvider(openfeature.ProviderConfig{
    DataSource: openfeature.DataSourceFlagRules,
    FlagRules: openfeature.FlagRulesConfig{
        URL:          "https://your-endpoint.example.com/flags.json",
        PollInterval: 60 * time.Second,  // Optional, default: 30s
    },
})
```

### Option 3: Custom HTTP Client

For advanced use cases (custom TLS, proxies, etc.):

```go
customClient := &http.Client{
    Timeout: 15 * time.Second,
    Transport: &http.Transport{
        // Custom transport configuration
    },
}

provider, err := openfeature.NewDatadogProvider(openfeature.ProviderConfig{
    DataSource: openfeature.DataSourceFlagRules,
    FlagRules: openfeature.FlagRulesConfig{
        URL:        "https://your-endpoint.example.com/flags.json",
        HTTPClient: customClient,
    },
})
```

## Configuration Reference

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED` | Must be `true` to enable the provider | `false` |
| `DD_FFE_FLAG_RULES_URL` | HTTP endpoint URL for flag configurations | (required) |
| `DD_FFE_FLAG_RULES_POLL_INTERVAL_SECONDS` | Polling interval in seconds | `30` |

### FlagRulesConfig Fields

| Field | Type | Description |
|-------|------|-------------|
| `URL` | `string` | HTTP endpoint URL (required if env var not set) |
| `PollInterval` | `time.Duration` | How often to poll for updates (default: 30s) |
| `HTTPClient` | `*http.Client` | Custom HTTP client (optional) |

## Response Format

The HTTP endpoint must return a JSON response in the universal flags configuration format:

```json
{
  "format": "SERVER",
  "createdAt": "2025-01-01T00:00:00Z",
  "environment": {
    "name": "production"
  },
  "flags": {
    "my-feature": {
      "key": "my-feature",
      "enabled": true,
      "variationType": "BOOLEAN",
      "variations": {
        "on": { "key": "on", "value": true },
        "off": { "key": "off", "value": false }
      },
      "allocations": [
        {
          "key": "default",
          "rules": [],
          "splits": [
            {
              "variationKey": "on",
              "shards": [
                {
                  "salt": "my-salt",
                  "ranges": [{ "start": 0, "end": 8192 }],
                  "totalShards": 8192
                }
              ]
            }
          ]
        }
      ]
    }
  }
}
```

## Caching and Conditional Requests

The flag rules backend supports HTTP conditional requests for efficient polling:

- **ETag**: If the server returns an `ETag` header, subsequent requests include `If-None-Match`
- **Last-Modified**: If the server returns `Last-Modified`, subsequent requests include `If-Modified-Since`
- **304 Not Modified**: The backend handles 304 responses and keeps the existing configuration

This minimizes bandwidth and server load when configurations haven't changed.

## Complete Example

```go
package main

import (
    "context"
    "log"
    "os"
    "time"

    "github.com/DataDog/dd-trace-go/v2/openfeature"
    of "github.com/open-feature/go-sdk/openfeature"
)

func main() {
    // Enable the provider
    os.Setenv("DD_EXPERIMENTAL_FLAGGING_PROVIDER_ENABLED", "true")

    // Create provider with flag rules backend
    provider, err := openfeature.NewDatadogProvider(openfeature.ProviderConfig{
        DataSource: openfeature.DataSourceFlagRules,
        FlagRules: openfeature.FlagRulesConfig{
            URL:          "https://cdn.example.com/flags.json",
            PollInterval: 30 * time.Second,
        },
    })
    if err != nil {
        log.Fatalf("Failed to create provider: %v", err)
    }
    defer provider.Shutdown()

    // Register with OpenFeature
    if err := of.SetProviderAndWait(provider); err != nil {
        log.Fatalf("Failed to set provider: %v", err)
    }

    // Use flags
    client := of.NewClient("my-app")
    ctx := context.Background()

    enabled, _ := client.BooleanValue(ctx, "my-feature", false, of.EvaluationContext{})
    log.Printf("my-feature enabled: %v", enabled)
}
```
