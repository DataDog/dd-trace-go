# fasthttp instrumentation

Use `WrapHandler` to trace a `fasthttp.RequestHandler` and enable AppSec checks
when AppSec is active. Pass the handler's `*fasthttp.RequestCtx` to
`appsec.MonitorParsedHTTPBody` and `appsec.MonitorHTTPResponseBody`.

## Timeout handlers

For an instrumented server, use the timeout functions from this integration,
not `github.com/valyala/fasthttp.TimeoutHandler` or `TimeoutWithCodeHandler`.
The native functions can return while their worker still changes the request
context. They do not provide the synchronization needed for tracing and AppSec
to read the response and clean up context values.

Both orders below are supported. **The request span and AppSec monitoring end
at the request deadline.** A timeout does not cancel the application handler. Body
monitoring and RASP checks made after the timeout do not run the WAF, and attacks
after that point are not added to this request's span. Application code must
manage cancellation if it must stop work at the deadline.

**Keep experimental span pooling disabled (the default).** These wrappers do not
support `tracer.WithSpanPool(true)`. A worker that continues after a timeout can
otherwise attach spans to another request's trace.

```go
import (
    "time"

    fasthttptrace "github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/v2"
    "github.com/valyala/fasthttp"
)

func tracedHandler(h fasthttp.RequestHandler) fasthttp.RequestHandler {
    return fasthttptrace.WrapHandler(
        fasthttptrace.TimeoutHandler(h, 2*time.Second, "request timed out"),
    )
}

func timedHandler(h fasthttp.RequestHandler) fasthttp.RequestHandler {
    return fasthttptrace.TimeoutHandler(
        fasthttptrace.WrapHandler(h), 2*time.Second, "request timed out",
        fasthttptrace.WithTimeoutConcurrency(64),
    )
}
```

`TimeoutHandler` sends status 408. `TimeoutWithCodeHandler` accepts a custom
status code. A non-positive duration disables the timeout wrapper.

The timeout owner starts and finishes tracing and AppSec operations. On timeout,
it checks a separate response, permits AppSec to replace that response, and
publishes it through `RequestCtx.TimeoutErrorWithResponse`. It does not read or
change the response that the application worker still uses. Context values are
restored only when that worker stops. The span status and error classification
use the response sent to the client.

Important behavior:

- A timeout does **not** cancel the application handler. The worker retains its
  request context until the handler returns. Application code must still obey
  fasthttp's restrictions on concurrent access to a request context.
- Nested timeout wrappers share one worker. The earliest active deadline sets
  the client response; the nested handler call itself runs until its handler
  returns. The first traced scope in the worker covers the whole timeout handler.
  Later traced calls in that worker are children of that request scope.
- Custom resource namers run before the handler starts, while the worker is
  paused, and again after the handler returns. A request that completes before
  its deadline gets the resource from the second call, as with `WrapHandler`
  alone. A request that times out keeps the resource from the first call, so
  it cannot include values that the handler sets later.
- The worker limit defaults to `fasthttp.DefaultConcurrency`, independently of
  `fasthttp.Server.Concurrency`. Set it with `WithTimeoutConcurrency`; timed-out
  workers retain their slots until they exit. Requests above the limit receive
  status 429. Reuse the returned handler to share one limit across requests.
  Nested wrappers use only the outer worker limit.
- Orchestrion can add the server tracing wrapper automatically. Use this
  integration's timeout functions in that case as well.
