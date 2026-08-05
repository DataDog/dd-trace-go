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

> The Service Extension need to be connected to a deployed [Datadog agent](https://docs.datadoghq.com/agent).

| Environment variable  | Default value | Description                      |
|-----------------------|---------------|----------------------------------|
| `DD_AGENT_HOST`       | `N/A`         | Host of a running Datadog Agent. |
| `DD_TRACE_AGENT_PORT` | `8126`        | Port of a running Datadog Agent. |

### Client IP resolution

App & API Protection identifies clients by IP address, both to attribute traces and to
enforce IP denylists. This works with no extra configuration: Google Cloud
[appends the address it observed](https://cloud.google.com/load-balancing/docs/https#x-forwarded-for_header)
to `X-Forwarded-For`, followed by the forwarding rule address, so the extension reads
the client from that fixed position rather than trusting whatever the client put in
front of it.

Forwarding the `source.ip` attribute is recommended but **not required**. It removes
the dependency on the header shape and is what the extension uses when present:

```yaml
forwardAttributes: [source.ip]
```

The Terraform equivalent, on `google_network_services_lb_traffic_extension` or
`google_network_services_lb_route_extension`:

```hcl
forward_attributes = ["source.ip"]
```

> Terraform support for `forward_attributes` needs a recent
> [`google-beta` provider](https://registry.terraform.io/providers/hashicorp/google-beta/latest/docs/resources/network_services_lb_traffic_extension);
> older versions reject the argument.

`source.address`, Envoy's own attribute carrying the same address as `host:port`, is
**not supported**: Google Cloud rejects it at configuration time with
`Error 400: invalid forward attribute source.address`, so there is nothing for the
extension to read.

Why this matters: a client connecting to the load balancer chooses what it sends in
`X-Forwarded-For`, so it can present an address that is not its own. The header the
load balancer delivers looks like

```text
X-Forwarded-For: <client-supplied>,<client observed by the load balancer>,<forwarding rule>
```

and generic resolution, which scans left to right and takes the first public address,
lands on the forged entry. Neither the positional rule nor `source.ip` is under the
client's control, so neither can be manipulated that way. The header itself is left
byte-for-byte intact so that App & API Protection still inspects exactly what was sent.

This applies only when the request is identified as a GCP Service Extension. In
addition, the positional rule requires `TrustGCLBXForwardedFor` on
`AppsecEnvoyConfig`; the published callout image enables it for its default GCP
deployment and disables it when `DD_SERVICE_EXTENSION_UDS_PATH` selects the documented
self-managed Envoy mode. Deployments identified as plain Envoy, Envoy Gateway or Istio
ignore both `source.ip` and the positional rule.

An address resolved this way becomes `http.client_ip`. The raw remote address remains
only a fallback for requests where the proxy could not determine `ClientIP`; the GCP
path does not report the load balancer's forwarding-rule address as
`network.client.ip`.

If `DD_TRACE_CLIENT_IP_HEADER` names a header, that header decides identity and none
of the above applies. Naming a header is an explicit statement about where the client
address lives, so it outranks both `source.ip` and the positional rule — which is what
makes it the right setting when a CDN in front of the load balancer is the only thing
that knows the real client.

Three limitations:

- **A CDN or proxy sits in front of the load balancer.** Both `source.ip` and the
  observed `X-Forwarded-For` entry describe the connection Google Cloud received, which
  in that topology comes from the CDN rather than from the end user. Recovering the
  original address then depends on whatever forwarding mechanism that CDN offers, and
  on trusting it; this integration does not attempt it.
- **Fewer than two `X-Forwarded-For` entries, with no `source.ip`.** There is then no
  load-balancer-appended entry to rely on, so resolution falls back to the generic
  behaviour of earlier releases. Zero-configuration protection therefore depends on the
  genuine `X-Forwarded-For` actually reaching the extension: if the extension is
  configured not to forward it, and `source.ip` is not forwarded either, then identity
  comes from whichever headers *are* forwarded and is client-selectable again.
- **The extension must only be reachable by the load balancer.** Both `source.ip` and the
  positional rule describe what the infrastructure reported, so anything able to call the
  callout's gRPC endpoint directly can present either and choose the identity that gets
  recorded and denylist-checked. Keep the callout backend restricted to traffic from your
  own proxy, as the default deployment does.

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
