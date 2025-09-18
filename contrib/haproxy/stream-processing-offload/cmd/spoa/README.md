# HAProxy Stream Processing Offload Agent (SPOA) with Datadog App & API Protection

[HAProxy SPOE](https://www.haproxy.com/blog/extending-haproxy-with-the-stream-processing-offload-engine) enable users to provide programmability and extensibility on HAProxy.

## Installation

### From Release

The images are published at each release of the tracer and can be found in [the repo registry](https://github.com/DataDog/dd-trace-go/pkgs/container/dd-trace-go%2Fhaproxy-spoa).

### Build image

The docker image can be build locally using docker. Start by cloning the `dd-trace-go` repo, `cd` inside it and run that command:
```sh
docker build -f contrib/haproxy/stream-processing-offload/cmd/spoa/Dockerfile -t datadog/dd-trace-go/haproxy-spoa:local .
```

## Configuration

The HAProxy SPOA agent expose some configuration:

| Environment variable                | Default value | Description                                                                                                                                 |
|-------------------------------------|---------------|---------------------------------------------------------------------------------------------------------------------------------------------|
| `DD_HAPROXY_SPOA_HOST`              | `0.0.0.0`     | Host on where the SPOA and HTTP server should listen to.                                                                                    |
| `DD_HAPROXY_SPOA_PORT`              | `3000`        | Port used by the SPOA that accept communication with HAProxy.                                                                               |
| `DD_HAPROXY_SPOA_HEALTHCHECK_PORT`  | `3080`        | Port used for the HTTP server for the health check.                                                                                         |
| `DD_APPSEC_BODY_PARSING_SIZE_LIMIT` | `0`           | Maximum size of the bodies to be processed in bytes. If set to 0, the bodies are not processed. The recommended value is `10000000` (10MB). | 
|

> The HAProxy SPOA need to be connected to a deployed [Datadog agent](https://docs.datadoghq.com/agent).

| Environment variable  | Default value | Description                      |
|-----------------------|---------------|----------------------------------|
| `DD_AGENT_HOST`       | `localhost`   | Host of a running Datadog Agent. |
| `DD_TRACE_AGENT_PORT` | `8126`        | Port of a running Datadog Agent. |

