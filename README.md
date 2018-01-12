[![CircleCI](https://circleci.com/gh/DataDog/dd-trace-go/tree/master.svg?style=svg)](https://circleci.com/gh/DataDog/dd-trace-go/tree/master)
[![Godoc](http://img.shields.io/badge/godoc-reference-blue.svg?style=flat)](https://godoc.org/github.com/DataDog/dd-trace-go/opentracing)

Datadog APM client that implements an [OpenTracing](http://opentracing.io) Tracer.

## Basic Usage Example

To start using the Datadog Tracer with the OpenTracing API, you should first initialize the tracer with a proper `Configuration` object:

```go
import (
	// ddtrace namespace is suggested
	ddtrace "github.com/DataDog/dd-trace-go/opentracing"
	"github.com/opentracing/opentracing-go"
)

func main() {
	// create a Tracer configuration
	config := ddtrace.NewConfiguration()
	config.ServiceName = "api-intake"
	config.AgentHostname = "ddagent.consul.local"

	// initialize a Tracer and ensure a graceful shutdown
	// using the `closer.Close()`
	tracer, closer, err := ddtrace.NewTracer(config)
	if err != nil {
		// handle the configuration error
	}
	defer closer.Close()

	// set the Datadog tracer as a GlobalTracer
	opentracing.SetGlobalTracer(tracer)
	startWebServer()
}
```

Function `NewTracer(config)` returns an `io.Closer` instance that can be used to gracefully shutdown the `tracer`. It's recommended to always call the `closer.Close()`, otherwise internal buffers are not flushed and you may lose some traces.

See the [Opentracing documentation](https://github.com/opentracing/opentracing-go) for more usage patterns. Legacy documentation is available in [GoDoc format](https://godoc.org/github.com/DataDog/dd-trace-go/tracer).

### Testing

To start a minimal environment needed to run the tests locally or on CI, we've set up
a `docker-compose` configuration which you may start by running the following command:

```
$ docker-compose up -d
```

Afterwards, you may test the package using the standard go toolchain.

## Further Reading

Automatically traced libraries and frameworks: https://godoc.org/github.com/DataDog/dd-trace-go/tracer#pkg-subdirectories
Sample code: https://godoc.org/github.com/DataDog/dd-trace-go/tracer#pkg-examples
