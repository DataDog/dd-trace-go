Override for `reviewers/security.md` (in the core skill folder) — read that file first, then this.

# Security — dd-trace-go specifics

This file starts with one confirmed pattern and should grow — add the next
one you learn from review. Do not treat it as exhaustive.

## Secrets must not become span tags or log lines

Never write any of the following into a span tag, a metric, or a log line:

- `DD_API_KEY` / `DD_APP_KEY` / `cfg.APIKey` / any similarly named config field
- A DSN or URL with an embedded username and password (`amqp://user:password@host:5672`)
- `Authorization`, `Cookie`, or other credential-bearing headers

`span.SetTag("dsn", cfg.DSN)` and `log.Printf("connecting to %s", cfg.DSN)` are the same finding: the secret leaves the process and becomes customer-visible. Treat it as **P0**.

Safe alternatives: tag the host / port / scheme only, or a redacted DSN (`amqp://***@host:5672`). Do not log the raw value "at debug, it's fine" — debug logs ship.

Look for this around contrib connect / authenticate / client-init paths (`contrib/**`) and anything that reads `internal/env` or a connection config struct.

## How to add the next rule

1. Write the pattern here in the same shape: what it looks like, why it matters, the concrete fix.
2. Add a case in `.llm-validation/suites/dd-apm-sdk-review.yaml` that would fail if this paragraph disappeared.
3. That is the whole contribution.
