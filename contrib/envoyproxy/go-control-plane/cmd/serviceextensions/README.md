# ASM Service Extension

[GCP Services Extensions](https://cloud.google.com/service-extensions/docs/overview) enable Google Cloud users to provide programmability and extensibility on Cloud Load Balancing data paths and at the edge.

## Installation

### From Release

This package provides a docker image to be used with Google Cloud Service Extensions.
The images are published at each release of the tracer and can be found in [the repo registry](https://github.com/DataDog/dd-trace-go/pkgs/container/dd-trace-go%2Fservice-extensions-callout).

### Build image

The docker image can be build locally using docker. Start by cloning the `dd-trace-go` repo, `cd` inside it and run that command:
```sh
docker build --build-arg -f contrib/envoyproxy/go-control-plane/cmd/serviceextensions/Dockerfile -t datadog/dd-trace-go/service-extensions-callout:local .
```

## Configuration

The ASM Service Extension expose some configuration. The configuration can be tweaked if the Service Extension is only used as an External Processor for Envoy that is not operated by GCP.

>**GCP requires that the default configuration for the Service Extension should not change.**

| Environment variable                      | Default value   | Description                                                                                                   |
|-------------------------------------------|-----------------|---------------------------------------------------------------------------------------------------------------|
| `DD_SERVICE_EXTENSION_HOST`               | `0.0.0.0`       | Host on where the gRPC and HTTP server should listen to.                                                      |
| `DD_SERVICE_EXTENSION_PORT`               | `443`           | Port used by the gRPC Server.<br>Envoy Google backend’s is only using secure connection to Service Extension. |
| `DD_SERVICE_EXTENSION_HEALTHCHECK_PORT`   | `80`            | Port used for the HTTP server for the health check.                                                           |
| `DD_SERVICE_EXTENSION_OBSERVABILITY_MODE` | `false`         | Enable observability mode. This will process a request asynchronously (blocking would be disabled).           |
| `DD_APPSEC_BODY_PARSING_SIZE_LIMIT`       | `10485760`      | Maximum size of the bodies to be processed in bytes. If set to 0, the bodies are not processed.               |
| `DD_SERVICE_EXTENSION_TLS`                | `true`          | Enable the gRPC TLS layer. Do not modify if you are using GCP.                                                |
| `DD_SERVICE_EXTENSION_TLS_KEY_FILE`       | `localhost.key` | Change the default gRPC TLS layer key. Do not modify if you are using GCP.                                    |
| `DD_SERVICE_EXTENSION_TLS_CERT_FILE`      | `localhost.crt` | Change the default gRPC TLS layer cert. Do not modify if you are using GCP.                                   |
| `DD_SERVICE_EXTENSION_UDS_PATH`           | _(unset)_       | Path to a Unix domain socket for the gRPC server. When set, overrides `DD_SERVICE_EXTENSION_HOST`/`PORT` and TLS is disabled. Only for self-managed Envoy deployments. |
| `DD_SERVICE_EXTENSION_INTEGRATION`        | _(unset)_       | Gateway in front of the extension: `gcp-service-extension`, `envoy`, `envoy-gateway` or `istio`. Leave unset for GCP. Set it to `envoy-gateway`, which cannot inject the identification header from its `EnvoyExtensionPolicy` CRD. |

> The Service Extension need to be connected to a deployed [Datadog agent](https://docs.datadoghq.com/agent).

| Environment variable  | Default value | Description                      |
|-----------------------|---------------|----------------------------------|
| `DD_AGENT_HOST`       | `N/A`         | Host of a running Datadog Agent. |
| `DD_TRACE_AGENT_PORT` | `8126`        | Port of a running Datadog Agent. |

### Client IP resolution

The extension identifies the address observed by Google Cloud rather than trusting the
first client-supplied `X-Forwarded-For` entry. Forwarding `source.ip` is recommended but
not required:

```yaml
forwardAttributes: [source.ip]
```

```hcl
forward_attributes = ["source.ip"]
```

> Terraform requires a recent
> [`google-beta` provider](https://registry.terraform.io/providers/hashicorp/google-beta/latest/docs/resources/network_services_lb_traffic_extension);
> `source.address` is not supported: GCP rejects it as an invalid forward attribute.

Without `source.ip`, GCLB's documented suffix is used:

```text
X-Forwarded-For: <client-supplied>,<client observed by the load balancer>,<forwarding rule>
```

The raw header remains unchanged for WAF inspection. `http.client_ip` receives the
trusted identity; `network.client.ip` continues to come from the transport
`RemoteAddr`. `DD_TRACE_CLIENT_IP_HEADER` takes precedence over both mechanisms.

The positional rule is enabled only for the published GCP deployment and disabled for
the documented self-managed UDS mode. Keep the callout endpoint restricted to your own
proxy. With a CDN in front, use its trusted client-IP header via
`DD_TRACE_CLIENT_IP_HEADER`. If neither `source.ip` nor the full GCLB header reaches the
extension, resolution falls back to the standard header policy.

### SSL Configuration

The Envoy of GCP is configured to communicate to the Service Extension with TLS.

`localhost` self signed certificates are generated and bundled into the App & API Protection Service Extension docker image and loaded at the start of the gRPC server.

### Unix Domain Socket

When running alongside a self-managed Envoy on the same host, the gRPC server can listen on a Unix domain socket instead of a TCP port. This avoids the need for TLS and reduces network overhead.

Set `DD_SERVICE_EXTENSION_UDS_PATH` to the desired socket path:

```sh
DD_SERVICE_EXTENSION_UDS_PATH=/var/run/dd-service-extension.sock ./service-extensions-callout
```

Configure Envoy to connect to the same socket:

```yaml
grpc_service:
  google_grpc:
    target_uri: "unix:///var/run/dd-service-extension.sock"
```

> **Note:** TLS (`DD_SERVICE_EXTENSION_TLS`) is ignored when a Unix domain socket is configured. Unix domain sockets are local to the host and do not require transport-layer encryption. This option is **not** supported with GCP Service Extensions.
