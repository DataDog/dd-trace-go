# Fiber v2 instrumentation

Use `Wrap` to trace a `*fiber.App` and enable AppSec checks when AppSec is
active. Orchestrion calls `Wrap` automatically.

```go
import (
    fibertrace "github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/v2"
    "github.com/gofiber/fiber/v2"
    fiberrecover "github.com/gofiber/fiber/v2/middleware/recover"
)

func newApp() *fiber.App {
    app := fibertrace.Wrap(fiber.New())
    // Register panic recovery immediately after Wrap.
    app.Use(fiberrecover.New())
    app.Get("/users/:id", func(c *fiber.Ctx) error {
        return c.SendString(c.Params("id"))
    })
    return app
}
```

## Set up the app with `Wrap`

Call `Wrap(app, opts...)` before you register middleware, routes, or mounted
apps. `Wrap` installs route guards during setup. Fiber's matcher gives the path
parameters to these guards before they call the user handlers. Thus, a block
on a path parameter prevents those handlers from running.

Important behavior:

- **Wrap each mounted app.** If a child app does not have its own `Wrap` call,
  its parameters are checked only after its handlers return. Then a block cannot
  prevent the side effects of those handlers.
- Mounted apps use the request span and options of the first wrapped app. This
  includes `WithIgnoreRequest`: if the first app ignores a request, mounted apps
  also skip tracing and AppSec.
- Consecutive route registrations can share a handler chain. Every appended
  handler is guarded.
- Repeated `Wrap` calls apply options, but do not add another middleware.
- Calls to `Wrap` on the same app must not run concurrently. All route
  registration must finish before the app serves requests.
- A block in a parameterized group middleware reports the route of that group,
  because the request has not reached the endpoint yet.
- Parsed bodies still require an explicit
  `appsec.MonitorParsedHTTPBody(c.UserContext(), body)` call. There is no
  automatic `BodyParser` hook.

### The older `Middleware` API

Use `Wrap` instead of `app.Use(fibertrace.Middleware())`. Do not install both.
The global `Middleware` form stays compatible, but it can only report endpoint
parameters after the handler chain. It cannot prevent the side effects of those
handlers.

## Request monitoring

The middleware starts the WAF operation before the handler chain. It keeps the
raw request target for WAF inspection. The WAF inspects all the query and cookie
pairs that fasthttp parses, including repeated keys. Fiber reads its values
(for example, with `c.Query` and `c.Cookies`) from these pairs. Invalid escapes,
semicolons, and cookie values that net/http rejects do not disable monitoring,
and do not remove values that the application can read.

## Errors

With AppSec enabled:

- The middleware renders returned errors through the error handler of the Fiber
  app, before it reports the response to the WAF. The error handler runs one
  time, only when necessary.
- The original error is recorded on the span. If AppSec blocks the response,
  the block is recorded instead.
- A pending block has priority over the error handler. It replaces the body and
  headers that the error handler sets.
- Earlier middleware receives `nil` from `c.Next()` for these handled errors.
  Put error logging or counting in the configured error handler.

With AppSec disabled, error propagation does not change. Fiber renders returned
errors after tracing returns. Thus, the span status can show the response before
the error, not the final status sent to the client.

## Panic recovery

Register panic-recovery middleware immediately after `Wrap` (or immediately
after `Middleware` if you use the older API), before other middleware and
routes. Recovery then runs inside the monitored handler chain, and the error
handler renders the response before the WAF inspects it.

**Recovery registered before tracing is not supported for AppSec panic
handling.** It runs too late: AppSec cannot inspect its final response, or
prevent it from replacing a pending block.

Tracing does not install or replace recovery. Panics that are not recovered
still propagate. Custom recovery callbacks stay under the control of the
application.

## Testing

From the `contrib/gofiber/fiber.v2` directory, run the tests and the benchmark:

```shell
go test ./...
go test -run '^$' -bench BenchmarkFiberMiddleware ./...
```

The tests cover parsing, error responses, blocking, and connection reuse.
`BenchmarkFiberMiddleware` measures requests with AppSec off and on. The
Orchestrion Fiber case in `internal/orchestrion/_integration/fiber.v2` verifies
blocking without manual middleware.
