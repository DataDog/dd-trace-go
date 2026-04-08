# Azure APIM / Boomi HTTP Callout Security Processor

An HTTP/JSON callout service for Azure API Management (APIM) and Boomi API Gateway that runs the Datadog Web Application Firewall (WAF) and returns access control decisions. The service processes requests through a configurable protocol supporting both 4-call (Azure APIM) and 2-call (Boomi) patterns.

## Deploy to Azure

[![Deploy to Azure](https://aka.ms/deploytoazurebutton)](https://portal.azure.com/#create/Microsoft.Template/uri/https%3A%2F%2Fraw.githubusercontent.com%2FDataDog%2Fdd-trace-go%2Fmain%2Fcontrib%2Fazure%2Fapim-callout%2Fdeploy%2Fazure%2Fazuredeploy.json)

One-click deployment of the complete APIM Callout infrastructure on Azure:
- **VNet** with subnets for ACA, ACI, and APIM integration
- **Azure Container Apps** running the callout service with KEDA HTTP autoscaling
- **Azure Container Instance** running the Datadog Agent (private subnet)
- **Private DNS** for internal FQDN resolution
- **APIM VNet integration** and optional policy injection

**Prerequisites:**
- An Azure subscription
- An existing Azure API Management service (Developer, Standard v2, or Premium tier)
- A [Datadog API key](https://app.datadoghq.com/organization-settings/api-keys)
- (Optional) An existing Log Analytics workspace for log collection

**Required parameters:**

| Parameter | Description |
|-----------|-------------|
| `datadogApiKey` | Your Datadog API key (stored securely). In the portal, you can click "Reference a Key Vault secret" to use an existing Key Vault secret instead of pasting the key directly. |
| `apimServiceName` | Name of your existing APIM service |

All other parameters have sensible defaults. See `deploy/azure/main.bicep` for the full parameter reference.

### Using as a Bicep module

Advanced users can reference the Bicep modules directly in their own IaC:

```bicep
module apimCallout 'path/to/contrib/azure/apim-callout/deploy/azure/main.bicep' = {
  name: 'dd-apim-callout'
  params: {
    datadogApiKey: keyVault.getSecret('dd-api-key') // Key Vault reference
    apimServiceName: 'my-apim'
    logAnalyticsWorkspaceId: logAnalytics.id
    // All other params have defaults — override as needed
  }
}
```

### Maintainer note

`deploy/azure/azuredeploy.json` is compiled from `main.bicep`. After modifying any Bicep file, regenerate it:

```bash
az bicep build -f deploy/azure/main.bicep --outfile deploy/azure/azuredeploy.json
```

## Overview

This service intercepts HTTP traffic at the gateway level, inspects request/response headers and bodies using Datadog AppSec, and returns one of two decisions:
- **Continue**: Allow traffic through with propagation headers (trace context, security tags)
- **Block**: Reject traffic with a custom status code, headers, and body

Single `POST /` endpoint handles all request/response phases through two-tier dispatch:
- **Tier 1**: New requests (no `request-id`) → run request headers phase + optional inline body
- **Tier 2**: Continuations (with `request-id`) → retrieve cached state + run appropriate phase

## Protocol

### Request Format

```json
{
  "addresses": { /* phase-dependent fields */ },
  "gateway": "boomi",
  "request-id": "550e8400-e29b-41d4-a716-446655440000",
  "phase": "request-headers"
}
```

**Fields:**
- `addresses` (object, required): Phase-dependent fields containing request/response data
- `gateway` (string, optional): Gateway identifier (`"boomi"` or default for APIM). Used for telemetry tagging only.
- `request-id` (string, optional): Absent on Phase 1 (new request), present on all subsequent phases
- `phase` (string, optional): Declared phase name for logging/validation. Not used for dispatch; actual phase determined by cached state.

**Addresses by phase:**

| Phase | Fields |
|-------|--------|
| Request Headers | `method`, `scheme`, `authority`, `path`, `remote_addr`, `headers`, (optional) `body` |
| Request Body | `body` (base64-encoded) |
| Response Headers | `status_code`, `headers`, (optional) `body` |
| Response Body | `body` (base64-encoded) |

### Response Format

```json
{
  "request-id": "550e8400-e29b-41d4-a716-446655440000",
  "propagate-headers": {
    "x-datadog-trace-id": ["123456789"],
    "x-datadog-parent-id": ["987654321"]
  },
  "allowed-body-size": 10485760,
  "block": {
    "status": 403,
    "headers": { "Content-Type": ["application/json"] },
    "content": "eyJlcnJvciI6ICJGb3JiaWRkZW4ifQ=="
  }
}
```

**Fields:**
- `request-id` (string): Present **only when absent from request** (Phase 1 response only). Use this ID in all subsequent callouts.
- `propagate-headers` (object): Trace context and security headers from Datadog. Inject these as request headers before forwarding to origin.
- `allowed-body-size` (number): Maximum body size (in bytes) the gateway may send. Present only when body analysis is enabled AND body is not provided inline AND request was not blocked.
- `block` (object): Blocking decision. Present only when request was blocked. Mutually exclusive with `propagate-headers` and `allowed-body-size`.

**blockResult fields:**
- `status` (number): HTTP status code to return to client
- `headers` (object): Response headers to set
- `content` (string): Base64-encoded response body

## Quick Start

### Docker

Pull and run the pre-built image:

```bash
docker run -d \
  -p 8080:8080 \
  -p 8081:8081 \
  -e DD_APPSEC_ENABLED=true \
  -e DD_AGENT_HOST=datadog-agent \
  -e DD_AGENT_APM_ENABLED=false \
  ghcr.io/datadog/dd-trace-go/apim-callout:latest
```

Health check on `GET http://localhost:8081/`:
```json
{
  "status": "ok",
  "library": {
    "language": "golang",
    "version": "2.x.x"
  }
}
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DD_APIM_CALLOUT_HOST` | `0.0.0.0` | Callout service listen address |
| `DD_APIM_CALLOUT_PORT` | `8080` | Callout service listen port |
| `DD_APIM_CALLOUT_HEALTHCHECK_PORT` | `8081` | Health check port (GET /) |
| `DD_APPSEC_BODY_PARSING_SIZE_LIMIT` | `104857600` | Max body size to parse (bytes). Set to 0 to disable body analysis. |
| `DD_APIM_CALLOUT_REQUEST_TIMEOUT` | `30s` | TTL for cached request state. Orphaned states are closed and memory released after this duration. |
| `DD_APIM_CALLOUT_TLS` | `false` | Enable HTTPS |
| `DD_APIM_CALLOUT_TLS_CERT_FILE` | `` | Path to TLS certificate file (required if `DD_APIM_CALLOUT_TLS=true`) |
| `DD_APIM_CALLOUT_TLS_KEY_FILE` | `` | Path to TLS private key file (required if `DD_APIM_CALLOUT_TLS=true`) |
| `DD_APPSEC_ENABLED` | `true` | Enable WAF |
| `DD_AGENT_HOST` | `localhost` | Datadog agent hostname for trace collection |
| `DD_AGENT_APM_ENABLED` | `false` | Send traces to agent (optional; usually `false` for callout service) |

## Gateway Integration

### Azure API Management (4-call pattern)

The service supports the full 4-phase request-response cycle:

1. **Inbound Phase 1**: `POST /` with request headers → receive `request-id`, check for block or `allowed-body-size`
2. **Inbound Phase 2** (conditional): `POST /` with request body + `request-id` (if body analysis enabled) → check for block
3. **Outbound Phase 3**: `POST /` with response headers + `request-id` → check for block or `allowed-body-size`
4. **Outbound Phase 4** (conditional): `POST /` with response body + `request-id` (if body analysis enabled) → check for block

See `deploy/azure/policies/` for APIM XML policy templates.

### Boomi API Gateway (2-call pattern)

Boomi sends bodies inline, so fewer callouts:

1. **Request phase**: `POST /` with request headers + body (optional) → receive `request-id`, check for block
2. **Response phase**: `POST /` with response headers + body (optional) + `request-id` → check for block

See `deploy/boomi/` for Boomi JSON + Groovy policy templates.

## Development

### Build Locally

```bash
go build -tags=appsec -o apim-callout ./cmd/apim-callout
```

### Run Tests

```bash
go test ./contrib/azure/apim-callout/...
```

### Build Docker Image

```bash
# Production image (runtime target)
docker build -o type=docker -t apim-callout:dev .

# E2E test image (includes test WAF rules)
docker build --target e2e -o type=docker -t apim-callout-e2e:dev .
```

### Example Usage

```go
package main

import (
	"context"
	"log"
	"net/http"

	apimcallout "github.com/DataDog/dd-trace-go/contrib/azure/apim-callout/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func main() {
	tracer.Start(tracer.WithAppSecEnabled(true))
	defer tracer.Stop()

	handler := apimcallout.NewHandler(apimcallout.AppsecAPIMConfig{
		Context: context.Background(),
	})

	log.Fatal(http.ListenAndServe(":8080", handler))
}
```

## Health Check

The service exposes a health check endpoint on a separate port:

**Request:**
```
GET http://localhost:8081/
```

**Response:**
```json
{
  "status": "ok",
  "library": {
    "language": "golang",
    "version": "2.56.0"
  }
}
```

The health check always returns `200 OK` if the service is running, regardless of WAF state.

## Fail-Open Behavior

The service applies fail-open semantics to maximize availability:

| Scenario | Response |
|----------|----------|
| Invalid JSON request | HTTP 400, body: `{}` |
| Cache miss (unknown `request-id`) | HTTP 200, body: `{}` (allow traffic) |
| Processor error | HTTP 200, body: `{}` (allow traffic) |
| Request timeout (TTL expired) | Orphaned state closed; gate returns allow |

## Request State Cache

The service maintains an in-memory TTL cache keyed by `request-id`:

- **Created**: Phase 1, when request is not blocked and more phases are needed
- **Kept**: Phases 2-3, while processor awaits next phase
- **Deleted**: After final phase or when blocked
- **Evicted**: After `DD_APIM_CALLOUT_REQUEST_TIMEOUT` (default 30s)

Each cached state includes a span with trace/security context. Memory is released immediately upon state closure or TTL expiration.

## Performance

### Callout Processing Time

Measured via APIM tracing on an Azure Container Apps deployment (0.5 vCPU, 1 GiB RAM):

| Phase | Processing Time |
|-------|----------------|
| Request headers (Phase 1) | ~2.4ms |
| Response headers (Phase 3) | ~2.4ms |
| Full APIM pipeline (2 callout phases + backend + policy evaluation) | ~9.7ms |

Body phases (2 and 4) are conditional and only execute when `allowed-body-size` is present in the previous phase response.

### APIM Policy Overhead

The APIM XML policies add minimal overhead per phase:
- JSON body construction (`set-body`): <0.1ms
- Response variable parsing: <0.1ms
- Conditional evaluation (`choose`): <0.01ms

The dominant factor in end-to-end latency is **network round-trip time** between the APIM gateway and the callout service, and between the client and the APIM gateway. Deploy the callout service in the same Azure region as your APIM instance, and ensure your APIM instance is in a region close to your API consumers. A 2048 connection pool is maintained by the gateway to the callout service so the numbers above may be lower than shown above in production.

## License

Apache License 2.0. See LICENSE file in the repository root.
