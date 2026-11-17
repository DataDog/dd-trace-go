**BEFORE writing or editing ANY code**, you MUST read [README.md](./README.md) for information about API changes and how to handle them.

For information about the tracer's abilities, you MUST read [doc.go](./doc.go).

## Updating Documentation

The developer should update [doc.go](./doc.go) with new, significant features of the core tracer. A feature may be considered significant when it allows the user to change the behavior of the tracer (ie APM sampling changes, rate limiting, etc).

Features of other products that are only configured through a tracer option (e.g. LLM Observability, AppSec) don't go in [doc.go](./doc.go). Document them in the godoc of that option and in the product's own package docs.

The developer should also update [README.md](./README.md) with important commands and scripts that enable tracer stability in CI.

If these updates are not made, tell the developer to make changes or provide suggestions if requested.